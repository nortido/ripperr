package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/manager/blackhole"
	"ripperr/internal/model"
)

type client interface {
	List(ctx context.Context, opts model.FilterOptions) (entries []*model.Entry, err error)
	GetTorrentDetails(ctx context.Context, hash string) (name, comment string, err error)
	IncomingHashSameAs(buf *bytes.Buffer, magnet string, existing string) (same bool, known bool)
	Exists(ctx context.Context, hash string) (bool, error)
	AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error)
	AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) (err error)
	Delete(ctx context.Context, opts model.ManagerDeleteOptions) (err error)
	DeletesFiles(ctx context.Context, opts model.ManagerDeleteOptions) bool
	WaitAbsent(ctx context.Context, hash string) (absent bool, err error)
	WriteBlackholePair(data []byte, magnet string, opts model.ManagerAddFileOptions) (err error)
	RemoveBlackholePair(hash string)
	ConfirmSettled(ctx context.Context, hash string) (settled bool)
	Discard(ctx context.Context, hash string) error
	Resume(ctx context.Context, hash string) error
	ReplacePending(hash string) bool
	Files(ctx context.Context, hash string) ([]model.TorrentFile, error)
	SetComment(ctx context.Context, hash, comment string) error
	ExportTorrent(ctx context.Context, hash string) ([]byte, error)
	DeleteEnabled() bool
}

const probeTimeout = 10 * time.Second // одна проба у локального клиента, дольше держать остановку незачем

type manager struct {
	client client
}

func (m *manager) GetEntries(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error) {
	return m.client.List(ctx, opts)
}

func (m *manager) Submit(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) (err error) {
	if buf == nil || buf.Len() == 0 {
		return
	}
	return m.replaceTorrent(ctx, buf, "", opts, func(opts model.ManagerAddFileOptions) error {
		return m.client.AddFromBuffer(ctx, buf, opts)
	})
}

func (m *manager) SubmitMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) (err error) {
	if magnet == "" {
		return
	}
	return m.replaceTorrent(ctx, nil, magnet, opts, func(opts model.ManagerAddFileOptions) error {
		return m.client.AddFromMagnet(ctx, magnet, opts)
	})
}

func (m *manager) replaceTorrent(ctx context.Context, buf *bytes.Buffer, magnet string, opts model.ManagerAddFileOptions, add func(model.ManagerAddFileOptions) error) (err error) {
	// имя и комментарий берем у текущего торрента до удаления, чтобы add получил готовые opts, а не сырые от вызывающего
	if opts.Hash != "" {
		if name, comment, detailErr := m.client.GetTorrentDetails(ctx, opts.Hash); detailErr == nil {
			if name != "" {
				opts.Name = name
			}
			if comment != "" && opts.Comment == "" {
				opts.Comment = comment
			}
		}
	}
	delOpts := model.ManagerDeleteOptions{Hash: opts.Hash, Category: opts.Category, Name: opts.Name}
	del := func() error {
		return m.client.Delete(ctx, delOpts)
	}
	var data []byte
	if buf != nil {
		data = buf.Bytes()
	}
	same, known := m.client.IncomingHashSameAs(buf, magnet, opts.Hash)
	newHash, hashKnown := incomingHash(data, magnet)
	// прошлая замена с удалением еще не доведена: пара жива по новому хешу или по хешу
	// записи, если это ее остановленный новый торрент. Любой исход тут запишет историю,
	// а подметальщик потом может откатить замену
	if (hashKnown && m.client.ReplacePending(newHash)) || (opts.Hash != "" && m.client.ReplacePending(opts.Hash)) {
		return fmt.Errorf("%w: %s", model.ErrReplacePending, opts.Name)
	}
	// выбор файлов читаем, пока старый торрент жив; ошибка чтения откладывает
	// замену до следующего прогона, иначе большая раздача скачалась бы целиком
	if len(data) > 0 && opts.Hash != "" && !same {
		files, filesErr := m.client.Files(ctx, opts.Hash)
		if filesErr != nil {
			return fmt.Errorf("read file selection of %s: %w", opts.Name, filesErr)
		}
		if slices.ContainsFunc(files, func(f model.TorrentFile) bool { return !f.Wanted }) {
			opts.FileSelection = files
		}
	}
	if known && !same {
		// старый торрент уйдет вместе с файлами: новый добавляем остановленным
		// и запускаем только после удаления старого, иначе общие файлы снесет
		if hashKnown && m.client.DeletesFiles(ctx, delOpts) {
			return m.replaceDeletingFirst(ctx, data, magnet, newHash, opts, del, add)
		}
	}
	if known && same {
		// байты те же, что уже раздаются. Если торрент правда в клиенте, delete+add даст только
		// полную перепроверку файлов на диске, выходим. Если проверка не ответила точно,
		// считаем что торрента нет и идем прежним защищенным путем
		if exists, existsErr := m.client.Exists(ctx, opts.Hash); existsErr == nil && exists {
			// осознанный размен: свежие категория, путь, тег, лимиты и комментарий из opts
			// до клиента не дойдут, обновить их без перезаливки нечем. Старые значения
			// проживут до следующей смены инфохеша
			return model.ErrHashUnchanged
		}
		if err = m.client.WriteBlackholePair(data, magnet, opts); err != nil {
			return
		}
		if err = del(); err != nil {
			return
		}
		if err = add(opts); err != nil {
			return
		}
		// удаление с файлами из del() в qbit еще может снести только что добавленный торрент.
		// Страховку снимаем, только если он пережил выжидание, иначе ее разберет подметальщик
		if !m.client.ConfirmSettled(ctx, opts.Hash) {
			return
		}
		m.client.RemoveBlackholePair(opts.Hash)
		return
	}
	if err = add(opts); err != nil {
		// с выбором клиент добавляет остановленным: созданный при сбое новый
		// иначе остался бы стоять, а следующий прогон получил бы ErrAlreadyAdded
		if len(opts.FileSelection) > 0 && hashKnown && !errors.Is(err, model.ErrAlreadyAdded) {
			m.rollbackNew(ctx, newHash)
		}
		return
	}
	return del()
}

func (m *manager) replaceDeletingFirst(ctx context.Context, data []byte, magnet, newHash string, opts model.ManagerAddFileOptions, del func() error, add func(model.ManagerAddFileOptions) error) (err error) {
	// новый торрент уже в клиенте: удаление старого с файлами снесло бы и его
	// общие файлы, прежний путь add-first тут отдавал ErrAlreadyAdded без удаления
	if present, existsErr := m.client.Exists(ctx, newHash); existsErr != nil {
		return existsErr
	} else if present {
		return fmt.Errorf("%w: %s", model.ErrAlreadyAdded, opts.Name)
	}
	// пара по новому хешу (подметальщик снимает ее после Settled по имени файла),
	// с ReplacesHash это метка незаконченной замены: по ней подметальщик запустит
	// новый торрент после сбоя или откатит его, если старый на месте
	pairOpts := opts
	pairOpts.Hash = newHash
	pairOpts.ReplacesHash = opts.Hash
	pairOpts.Stopped = false
	if err = m.client.WriteBlackholePair(data, magnet, pairOpts); err != nil {
		return
	}
	if err = ctx.Err(); err != nil {
		m.client.RemoveBlackholePair(newHash)
		return
	}
	// остановленный торрент не трогает файлы до запуска, а файлы старого
	// удаляем только когда новый уже точно в клиенте
	addOpts := opts
	addOpts.Stopped = true
	if err = add(addOpts); err != nil {
		if errors.Is(err, model.ErrAlreadyAdded) {
			m.client.RemoveBlackholePair(newHash)
			return
		}
		m.rollbackNew(ctx, newHash)
		return
	}
	if err = ctx.Err(); err != nil {
		m.rollbackNew(ctx, newHash)
		return
	}
	// дальше пара остается при любом сбое: старый мог уйти асинхронно, и
	// решать, запустить новый или откатить его, будет подметальщик
	if err = del(); err != nil {
		return
	}
	absent, err := m.client.WaitAbsent(ctx, opts.Hash)
	if err != nil {
		return
	}
	if !absent {
		return fmt.Errorf("old torrent %s still present after delete", opts.Name)
	}
	if err = sleepCtx(ctx, blackhole.SettleDelay); err != nil {
		return
	}
	if err = m.client.Resume(ctx, newHash); err != nil {
		return fmt.Errorf("start new torrent: %w", err)
	}
	if m.client.ConfirmSettled(ctx, newHash) {
		m.client.RemoveBlackholePair(newHash)
	}
	return nil
}

func (m *manager) rollbackNew(ctx context.Context, newHash string) {
	// отмена ctx (перезапуск) не должна сорвать откат; отсутствие или ошибка
	// пробы оставляют пару: запоздалое добавление уберет подметальщик
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeTimeout)
	defer cancel()
	if present, err := m.client.Exists(probeCtx, newHash); err != nil || !present {
		return
	}
	if m.client.Discard(probeCtx, newHash) != nil {
		return
	}
	m.client.RemoveBlackholePair(newHash)
}

func (m *manager) AttachComment(ctx context.Context, hash, comment string, fetch func(context.Context) ([]byte, error)) error {
	err := m.client.SetComment(ctx, hash, comment)
	// без разрешения удалять торренты перезаливки нет, ссылка остается только в памяти процесса
	if !errors.Is(err, model.ErrCommentUnsupported) || !m.client.DeleteEnabled() {
		return err
	}
	return m.readdWithComment(ctx, hash, comment, fetch)
}

func (m *manager) readdWithComment(ctx context.Context, hash, comment string, fetch func(context.Context) ([]byte, error)) (err error) {
	if m.client.ReplacePending(hash) {
		return fmt.Errorf("%w: %s", model.ErrReplacePending, hash)
	}
	entries, err := m.client.List(ctx, model.FilterOptions{})
	if err != nil && !errors.Is(err, model.ErrPartialList) {
		return err
	}
	i := slices.IndexFunc(entries, func(e *model.Entry) bool { return strings.EqualFold(e.Hash, hash) })
	if i < 0 {
		return fmt.Errorf("torrent %s not found", hash)
	}
	e := entries[i]
	// при ошибке или пропаже файлов перезаливка запустит полную закачку, а проверку или перенос оборвет
	if e.Unhealthy {
		return fmt.Errorf("torrent %s is errored, checking or moving, comment not attached", e.Name)
	}
	files, err := m.client.Files(ctx, hash)
	if err != nil {
		return fmt.Errorf("read file selection of %s: %w", e.Name, err)
	}
	// пустой выбор при добавлении значит качать все, такой торрент не трогаем
	if len(files) > 0 && !slices.ContainsFunc(files, func(f model.TorrentFile) bool { return f.Wanted }) {
		return fmt.Errorf("all files of %s unchecked, comment not attached", e.Name)
	}
	data, err := m.client.ExportTorrent(ctx, hash)
	if errors.Is(err, model.ErrExportUnsupported) {
		data, err = fetch(ctx)
	}
	if err != nil {
		return fmt.Errorf("get torrent %s: %w", e.Name, err)
	}
	if data, err = torrentmeta.SetComment(data, comment); err != nil {
		return fmt.Errorf("set comment of %s: %w", e.Name, err)
	}
	if got, hashErr := torrentmeta.InfoHash(data); hashErr != nil || !strings.EqualFold(got, hash) {
		return fmt.Errorf("torrent %s: infohash %q does not match, err %v", e.Name, got, hashErr)
	}
	opts := model.ManagerAddFileOptions{
		Hash:           hash,
		Name:           e.Name,
		Category:       e.Category,
		SavePath:       e.SavePath,
		Tag:            e.Tag,
		AutoManaged:    e.AutoManaged,
		MaxRatio:       e.MaxRatio,
		MaxSeedingTime: e.MaxSeedingTime,
		Comment:        comment,
		Stopped:        e.Stopped,
	}
	if slices.ContainsFunc(files, func(f model.TorrentFile) bool { return !f.Wanted }) {
		opts.FileSelection = files
	}
	if err = m.client.WriteBlackholePair(data, "", opts); err != nil {
		return
	}
	if err = ctx.Err(); err != nil {
		m.client.RemoveBlackholePair(hash)
		return
	}
	// дальше пара остается при любом сбое, торрент вернет подметальщик.
	// Discard всегда без файлов, клиент подхватит скачанное при добавлении
	if err = m.client.Discard(ctx, hash); err != nil {
		return
	}
	absent, err := m.client.WaitAbsent(ctx, hash)
	if err != nil {
		return
	}
	if !absent {
		return fmt.Errorf("torrent %s still present after remove", e.Name)
	}
	if err = m.client.AddFromBuffer(ctx, bytes.NewBuffer(data), opts); err != nil {
		return
	}
	if m.client.ConfirmSettled(ctx, hash) {
		m.client.RemoveBlackholePair(hash)
	}
	return nil
}

func incomingHash(data []byte, magnet string) (string, bool) {
	if len(data) > 0 {
		hash, err := torrentmeta.InfoHash(data)
		return hash, err == nil
	}
	if magnet != "" {
		return torrentmeta.BtihFromMagnet(magnet)
	}
	return "", false
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func NewManager(client client) *manager {
	return &manager{
		client: client,
	}
}
