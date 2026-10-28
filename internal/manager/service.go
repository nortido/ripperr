package manager

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ripperr/internal/helper/fileselect"
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
	Purge(ctx context.Context, hash string) error
	Resume(ctx context.Context, hash string) error
	ReplacePending(hash string) bool
	Files(ctx context.Context, hash string) ([]model.TorrentFile, error)
	FreeSpace(ctx context.Context, path string) (int64, error)
	SetComment(ctx context.Context, hash, comment string) error
	ExportTorrent(ctx context.Context, hash string) ([]byte, error)
	ApplySelection(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error
	DeleteEnabled() bool
}

const probeTimeout = 10 * time.Second // одна проба у локального клиента, дольше держать остановку незачем

var (
	// в сумме меньше freshPairGrace: подметальщик не должен застать пару, пока замена идет
	replaceAddBudget   = blackhole.AddTimeout
	replaceStartBudget = blackhole.CheckWaitBudget
)

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
	return m.replaceTorrent(ctx, buf, opts, func(ctx context.Context, opts model.ManagerAddFileOptions) error {
		return m.client.AddFromBuffer(ctx, buf, opts)
	})
}

func (m *manager) replaceTorrent(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions, add func(context.Context, model.ManagerAddFileOptions) error) (err error) {
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
	data := buf.Bytes()
	same, known := m.client.IncomingHashSameAs(buf, "", opts.Hash)
	newHash, hashKnown := incomingHash(data)
	// прошлая замена с удалением еще не доведена: пара жива по новому хешу или по хешу
	// записи, если это ее остановленный новый торрент. Любой исход тут запишет историю,
	// а подметальщик потом может откатить замену
	if (hashKnown && m.client.ReplacePending(newHash)) || (opts.Hash != "" && m.client.ReplacePending(opts.Hash)) {
		return fmt.Errorf("%w: %s", model.ErrReplacePending, opts.Name)
	}
	var oldFiles []model.TorrentFile
	// выбор файлов читаем, пока старый торрент жив; ошибка чтения откладывает
	// замену до следующего прогона, иначе большая раздача скачалась бы целиком
	if opts.Hash != "" && !same {
		files, filesErr := m.client.Files(ctx, opts.Hash)
		if filesErr != nil {
			return fmt.Errorf("read file selection of %s: %w", opts.Name, filesErr)
		}
		oldFiles = files
		if slices.ContainsFunc(files, func(f model.TorrentFile) bool { return !f.Wanted }) {
			opts.FileSelection = files
		}
	}
	if !known || !same {
		// одно решение и для выбора пути, и для Delete: повторная проверка в клиенте
		// (transmission спрашивает downloadDir) могла бы ответить иначе
		delOpts.DeleteFiles = m.client.DeletesFiles(ctx, delOpts)
	}
	if opts.Hash != "" && !same && hashKnown {
		if err = m.checkSpace(ctx, data, newHash, oldFiles, opts, known && delOpts.DeleteFiles); err != nil {
			return
		}
	}
	if known && !same {
		// старый торрент уйдет вместе с файлами: новый добавляем остановленным
		// и запускаем только после удаления старого, иначе общие файлы снесет
		if hashKnown && delOpts.DeleteFiles {
			return m.replaceDeletingFirst(ctx, data, newHash, opts, delOpts, add)
		}
	}
	if known && same {
		// байты те же, что уже раздаются. Если торрент правда в клиенте, delete+add даст только
		// полную перепроверку файлов на диске, выходим. Ошибка проверки ничего не трогает,
		// повторит следующий прогон. Нет торрента: перезаливаем без удаления файлов, они общие
		exists, existsErr := m.client.Exists(ctx, opts.Hash)
		if existsErr != nil {
			return fmt.Errorf("check presence of %s: %w", opts.Name, existsErr)
		}
		if exists {
			// осознанный размен: свежие категория, путь, тег, лимиты и комментарий из opts
			// до клиента не дойдут, обновить их без перезаливки нечем. Старые значения
			// проживут до следующей смены инфохеша
			return model.ErrHashUnchanged
		}
		if err = m.client.WriteBlackholePair(data, "", opts); err != nil {
			return
		}
		if err = del(); err != nil {
			return
		}
		if err = add(ctx, opts); err != nil {
			return
		}
		// асинхронное удаление из del() в qbit еще может снести только что добавленный торрент.
		// Страховку снимаем, только если он пережил выжидание, иначе ее разберет подметальщик
		if !m.client.ConfirmSettled(ctx, opts.Hash) {
			return
		}
		m.client.RemoveBlackholePair(opts.Hash)
		return
	}
	selected := len(opts.FileSelection) > 0 && hashKnown
	if selected {
		// откат ниже убирает новый по хешу: без этой пробы он снес бы торрент,
		// который был в клиенте до этого вызова
		present, existsErr := m.client.Exists(ctx, newHash)
		if existsErr != nil {
			return fmt.Errorf("check presence of new torrent for %s: %w", opts.Name, existsErr)
		}
		if present {
			return m.convergeSelected(ctx, delOpts, newHash, opts, fmt.Errorf("%w: %s", model.ErrAlreadyAdded, opts.Name))
		}
	}
	// пара замены живет только вокруг add: если клиент добавит новый позже ошибки,
	// подметальщик откатит его при живом старом, даже когда запись больше не проверяют
	paired := selected && opts.Hash != "" && !strings.EqualFold(newHash, opts.Hash)
	addCtx := ctx
	var start time.Time
	if paired {
		pairOpts := opts
		pairOpts.Hash = newHash
		pairOpts.ReplacesHash = opts.Hash
		pairOpts.Stopped = false
		if err = m.client.WriteBlackholePair(data, "", pairOpts); err != nil {
			return
		}
		start = time.Now()
		if err = ctx.Err(); err != nil {
			m.client.RemoveBlackholePair(newHash)
			return
		}
		var cancelAdd context.CancelFunc
		addCtx, cancelAdd = context.WithDeadline(ctx, start.Add(replaceAddBudget))
		defer cancelAdd()
	}
	if err = add(addCtx, opts); err != nil {
		if errors.Is(err, model.ErrAlreadyAdded) {
			if selected {
				if paired {
					m.client.RemoveBlackholePair(newHash)
				}
				return m.convergeSelected(ctx, delOpts, newHash, opts, err)
			}
			return m.dropReplaced(ctx, delOpts, newHash, hashKnown, err)
		}
		// с выбором клиент добавляет остановленным: созданный при сбое новый
		// иначе остался бы стоять, а следующий прогон получил бы ErrAlreadyAdded
		if selected {
			m.rollbackNew(ctx, newHash)
		}
		return
	}
	if paired {
		// добавленный позже срока новый подметальщик уже может откатывать
		if time.Since(start) >= replaceAddBudget {
			m.rollbackNew(ctx, newHash)
			return fmt.Errorf("add of new torrent for %s exceeded %s, old kept", opts.Name, replaceAddBudget)
		}
		m.client.RemoveBlackholePair(newHash)
	}
	return del()
}

func (m *manager) dropReplaced(ctx context.Context, delOpts model.ManagerDeleteOptions, newHash string, hashKnown bool, cause error) error {
	// при неизвестном или том же хеше в клиенте может быть сам новый торрент
	if !hashKnown || delOpts.Hash == "" || strings.EqualFold(newHash, delOpts.Hash) {
		return cause
	}
	// новый уже в клиенте и может стоять на тех же путях, файлы старого не трогаем
	delOpts.DeleteFiles = false
	if err := m.client.Delete(ctx, delOpts); err != nil {
		return fmt.Errorf("remove replaced torrent %s: %w", delOpts.Name, err)
	}
	return cause
}

func (m *manager) convergeSelected(ctx context.Context, delOpts model.ManagerDeleteOptions, newHash string, opts model.ManagerAddFileOptions, cause error) error {
	if delOpts.Hash == "" || strings.EqualFold(newHash, delOpts.Hash) {
		return cause
	}
	entries, err := m.client.List(ctx, model.FilterOptions{})
	if err != nil && !errors.Is(err, model.ErrPartialList) {
		return cause
	}
	i := slices.IndexFunc(entries, func(e *model.Entry) bool { return strings.EqualFold(e.Hash, newHash) })
	// сбойный новый выбор не примет, старый не трогаем, он раздает дальше
	if i < 0 || entries[i].Unhealthy {
		return cause
	}
	if !entries[i].Stopped {
		// запущенный новый с тем же выбором значит, что прошлый прогон применил выбор,
		// но не удалил старый. Иной выбор: новый качает лишнее, старый оставляем
		if !m.selectionApplied(ctx, newHash, opts.FileSelection) {
			return cause
		}
		return m.dropReplaced(ctx, delOpts, newHash, true, cause)
	}
	if err = m.client.ApplySelection(ctx, newHash, opts); err != nil {
		return fmt.Errorf("apply file selection to new torrent for %s: %w", opts.Name, err)
	}
	return m.dropReplaced(ctx, delOpts, newHash, true, cause)
}

func (m *manager) selectionApplied(ctx context.Context, newHash string, selection []model.TorrentFile) bool {
	files, err := m.client.Files(ctx, newHash)
	if err != nil || len(files) == 0 {
		return false
	}
	paths := make([]string, len(files))
	var unwanted []int
	for i, f := range files {
		paths[i] = f.Path
		if !f.Wanted {
			unwanted = append(unwanted, i)
		}
	}
	return slices.Equal(unwanted, fileselect.Unwanted(selection, paths))
}

func (m *manager) replaceDeletingFirst(ctx context.Context, data []byte, newHash string, opts model.ManagerAddFileOptions, delOpts model.ManagerDeleteOptions, add func(context.Context, model.ManagerAddFileOptions) error) (err error) {
	// новый торрент уже в клиенте: удаление старого с файлами снесло бы и его общие файлы
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
	if err = m.client.WriteBlackholePair(data, "", pairOpts); err != nil {
		return
	}
	start := time.Now()
	if err = ctx.Err(); err != nil {
		m.client.RemoveBlackholePair(newHash)
		return
	}
	// остановленный торрент не трогает файлы до запуска, а файлы старого
	// удаляем только когда новый уже точно в клиенте
	addOpts := opts
	addOpts.Stopped = true
	addCtx, cancelAdd := context.WithDeadline(ctx, start.Add(replaceAddBudget))
	err = add(addCtx, addOpts)
	cancelAdd()
	if err != nil {
		if errors.Is(err, model.ErrAlreadyAdded) {
			m.client.RemoveBlackholePair(newHash)
			return
		}
		m.rollbackNew(ctx, newHash)
		return
	}
	// клиент мог не заметить отмену и добавить позже срока: после freshPairGrace подметальщик
	// откатит новый при живом старом, а мы бы в это время удаляли старый с файлами
	if time.Since(start) >= replaceAddBudget {
		m.rollbackNew(ctx, newHash)
		return fmt.Errorf("add of new torrent for %s exceeded %s, old kept", opts.Name, replaceAddBudget)
	}
	if err = ctx.Err(); err != nil {
		m.rollbackNew(ctx, newHash)
		return
	}
	finishCtx, cancel := context.WithDeadline(ctx, start.Add(replaceAddBudget+replaceStartBudget))
	defer cancel()
	// дальше пара остается при любом сбое: старый мог уйти асинхронно, и
	// решать, запустить новый или откатить его, будет подметальщик
	if err = m.client.Delete(finishCtx, delOpts); err != nil {
		return
	}
	absent, err := m.client.WaitAbsent(finishCtx, opts.Hash)
	if err != nil {
		return
	}
	if !absent {
		return fmt.Errorf("old torrent %s still present after delete", opts.Name)
	}
	if err = sleepCtx(finishCtx, blackhole.SettleDelay); err != nil {
		return
	}
	if err = m.client.Resume(finishCtx, newHash); err != nil {
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

func incomingHash(data []byte) (string, bool) {
	hash, err := torrentmeta.InfoHash(data)
	return hash, err == nil
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
