package blackhole

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

const (
	sweepInterval = 5 * time.Minute
	// без потолка зависший клиент навсегда держит горутину подметания, а app.Run
	// ждет ее перед выходом, так что зависает и перезагрузка
	reachableTimeout = 30 * time.Second
	// страховка от зависшего добавления. У qbit путь с комментарием ждет в одном ctx подряд magnetFetchTimeout
	// (1 мин) и metadataFetchTimeout (5 мин). Если тут меньше суммы с запасом, запасной путь упрется в дедлайн,
	// вернет DeadlineExceeded, путь без комментария не запустится и торрент не добавится вообще. Сумму сторожит тест в qbittorrent
	AddTimeout      = 7 * time.Minute
	CheckWaitBudget = 2 * time.Minute
	// свежая пара еще в работе у записавшего: replaceTorrent добавляет новый
	// остановленным (до AddTimeout) и запускает его (Resume до CheckWaitBudget),
	// раньше конца окна подметальщик мог бы откатить торрент, который вот-вот запустят
	freshPairGrace = AddTimeout + CheckWaitBudget + time.Minute
)

var SettleDelay = 3 * time.Second // переменная, чтобы тесты могли ужать

// клиент после своего рестарта может минутами отдавать неполный список, и пауза
// SettleDelay его не переждет. Отсутствие старого принимаем только со второго тика
var confirmAbsentAfter = sweepInterval / 2

type client interface {
	AddFromBuffer(ctx context.Context, buf *bytes.Buffer, opts model.ManagerAddFileOptions) error
	AddFromMagnet(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error
	Exists(ctx context.Context, hash string) (bool, error)
	Reachable(ctx context.Context) bool
	Discard(ctx context.Context, hash string) error
	Resume(ctx context.Context, hash string) error
}

type Store struct {
	dir    string
	client client
	// новый хеш замены -> когда старый впервые не нашелся; трогает только горутина подметания
	absentSeen map[string]time.Time
}

func New(dir string, c client) *Store {
	return &Store{dir: dir, client: c}
}

func blackholePaths(dir, hash string) (torrentPath, jsonPath string) {
	hash = strings.ToLower(hash)
	return filepath.Join(dir, hash+".torrent"), filepath.Join(dir, hash+".json")
}

type blackholeMeta struct {
	model.ManagerAddFileOptions
	Magnet string
}

func (s *Store) Write(data []byte, magnet string, opts model.ManagerAddFileOptions) error {
	if s.dir == "" {
		return fmt.Errorf("blackhole directory not configured")
	}
	if opts.Hash == "" {
		return fmt.Errorf("blackhole write: missing hash")
	}
	if len(data) == 0 && magnet == "" {
		return fmt.Errorf("blackhole write: no data or magnet to protect")
	}
	meta, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: opts, Magnet: magnet})
	if err != nil {
		return fmt.Errorf("marshal blackhole metadata: %w", err)
	}
	torrentPath, jsonPath := blackholePaths(s.dir, opts.Hash)
	wroteTorrent := false
	if len(data) > 0 {
		if err = writeFileAtomic(torrentPath, data); err != nil {
			return fmt.Errorf("write blackhole torrent: %w", err)
		}
		wroteTorrent = true
	}
	if err = writeFileAtomic(jsonPath, meta); err != nil {
		// не оставляем осиротевший .torrent после неудачной записи, для
		// подметальщика он навсегда выглядел бы сломанной парой
		if wroteTorrent {
			_ = os.Remove(torrentPath)
		}
		return fmt.Errorf("write blackhole metadata: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	// у пары с одним магнитом json это вся страховка, а os.WriteFile потеряет ее при
	// падении посреди записи. Пишем во временный файл, fsync, потом rename поверх
	// CreateTemp: 0600 (в паре passkey) и свое имя на писателя, общий .tmp ломал параллельную запись
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err = f.Sync(); err != nil {
		// fsync умеет не каждая ФС, запись из-за него не валим. Для rename ниже
		// отдельно делается fsync каталога
		logger.Warn(fmt.Sprintf("blackhole: fsync %s: %v", tmp, err))
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// пока запись каталога не сброшена, падение сразу после Rename на некоторых ФС
	// теряет файл, хотя Rename вернул успех. Как и fsync файла, не фатально
	if err = syncDir(filepath.Dir(path)); err != nil {
		logger.Warn(fmt.Sprintf("blackhole: fsync dir %s: %v", filepath.Dir(path), err))
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

func removeBlackholePair(dir, hash string) {
	torrentPath, jsonPath := blackholePaths(dir, hash)
	_ = os.Remove(torrentPath)
	_ = os.Remove(jsonPath)
}

func (s *Store) Remove(hash string) {
	removeBlackholePair(s.dir, hash)
}

func (s *Store) Settled(ctx context.Context, hash string) bool {
	// удаление с файлами идет у клиента асинхронно: торрент уходит из списка сразу, а задача
	// еще может снести его снова, ранний повтор того же infohash клиент молча отвергает. Ждем
	// SettleDelay, как ForceRedownload, и на общем ctx, ctx добавления вызывающий уже отменил
	if err := sleepCtx(ctx, SettleDelay); err != nil {
		logger.Warn(fmt.Sprintf("blackhole: %s settle wait interrupted: %v, keeping record for a later retry", hash, err))
		return false
	}
	present, err := s.client.Exists(ctx, hash)
	if err != nil {
		logger.Warn(fmt.Sprintf("blackhole: %s presence recheck failed: %v, keeping record for a later retry", hash, err))
		return false
	}
	if !present {
		logger.Warn(fmt.Sprintf("blackhole: %s not confirmed present after settle delay, keeping record for a later retry", hash))
		return false
	}
	return true
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

type blackholePair struct {
	hash    string
	torrent []byte
	magnet  string
	opts    model.ManagerAddFileOptions
	modTime time.Time
}

const reasonHashMismatch = "hash mismatch"

type blackholeBroken struct {
	hash    string
	reason  string
	opts    model.ManagerAddFileOptions
	modTime time.Time
}

func scanBlackholeDir(dir string) (pairs []blackholePair, broken []blackholeBroken, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}

	torrents := map[string]string{}
	jsons := map[string]string{}
	modTimes := map[string]time.Time{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// файлы с любым другим расширением не трогаем вообще, даже не логируем
		switch ext := filepath.Ext(name); ext {
		case ".torrent":
			// нижний регистр, как в blackholePaths, иначе пару со смешанным регистром
			// подметальщик оживлял бы каждый тик и никогда не смог удалить
			torrents[strings.ToLower(strings.TrimSuffix(name, ext))] = filepath.Join(dir, name)
		case ".json":
			hash := strings.ToLower(strings.TrimSuffix(name, ext))
			jsons[hash] = filepath.Join(dir, name)
			if info, infoErr := e.Info(); infoErr == nil {
				modTimes[hash] = info.ModTime()
			}
		}
	}

	for hash, jPath := range jsons {
		tPath, hasTorrent := torrents[hash]
		data, magnet, opts, reason := loadBlackholePair(hash, jPath, tPath, hasTorrent)
		if reason != "" {
			broken = append(broken, blackholeBroken{hash: hash, reason: reason, opts: opts, modTime: modTimes[hash]})
			continue
		}
		pairs = append(pairs, blackholePair{hash: hash, torrent: data, magnet: magnet, opts: opts, modTime: modTimes[hash]})
	}
	for hash := range torrents {
		if _, ok := jsons[hash]; !ok {
			broken = append(broken, blackholeBroken{hash: hash, reason: "missing .json"})
		}
	}
	return pairs, broken, nil
}

func loadBlackholePair(hash, jsonPath, torrentPath string, hasTorrent bool) (data []byte, magnet string, opts model.ManagerAddFileOptions, reason string) {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, "", opts, fmt.Sprintf("read json: %v", err)
	}
	if len(raw) == 0 {
		return nil, "", opts, "empty json file"
	}
	var meta blackholeMeta
	if err = json.Unmarshal(raw, &meta); err != nil {
		return nil, "", opts, fmt.Sprintf("parse json: %v", err)
	}
	// опции встроены плоско, так что старый .json без Magnet тоже читается, миграция не нужна
	opts = meta.ManagerAddFileOptions
	// "null" и "{}" разбираются без ошибки в пустые opts. Ловим тут, иначе восстановим
	// торрент без категории, тегов и имени
	if !strings.EqualFold(meta.Hash, hash) {
		return nil, "", opts, fmt.Sprintf("%s: meta hash %q, file %s", reasonHashMismatch, meta.Hash, hash)
	}

	if !hasTorrent {
		if meta.Magnet == "" {
			return nil, "", opts, "missing .torrent"
		}
		return nil, meta.Magnet, opts, ""
	}
	data, err = os.ReadFile(torrentPath)
	if err != nil {
		// битый .torrent при живом магните еще восстановим, но ошибку чтения пишем в лог:
		// другого сигнала об умирающем диске до поломки всех пар не будет
		if meta.Magnet != "" {
			logger.Warn(fmt.Sprintf("blackhole %s: read torrent: %v, falling back to magnet", hash, err))
			return nil, meta.Magnet, opts, ""
		}
		return nil, "", opts, fmt.Sprintf("read torrent: %v", err)
	}
	if len(data) == 0 {
		if meta.Magnet != "" {
			return nil, meta.Magnet, opts, ""
		}
		return nil, "", opts, "empty torrent file"
	}
	// непустой .torrent может быть обрезан падением посреди записи. Тогда берем магнит,
	// иначе AddFromBuffer получит байты, на которых InfoHash снова упадет
	if _, err = torrentmeta.InfoHash(data); err != nil {
		if meta.Magnet != "" {
			logger.Warn(fmt.Sprintf("blackhole %s: parse torrent: %v, falling back to magnet", hash, err))
			return nil, meta.Magnet, opts, ""
		}
		return nil, "", opts, fmt.Sprintf("parse torrent: %v", err)
	}
	return data, meta.Magnet, opts, ""
}

func (s *Store) Sweep(ctx context.Context) {
	if s.dir == "" {
		return
	}
	s.SweepOnce(ctx)
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.SweepOnce(ctx)
		}
	}
}

func (s *Store) SweepOnce(ctx context.Context) {
	// запись переживает только тик, на котором ее подтвердили: недоступный клиент,
	// ошибка пробы или пропавшая пара начинают подтверждение заново
	prevAbsent := s.absentSeen
	s.absentSeen = map[string]time.Time{}
	// одна проба доступности на тик: нет смысла обходить каталог (и логировать
	// сломанные пары каждый тик), пока клиент загрузок просто лежит
	probeCtx, cancel := context.WithTimeout(ctx, reachableTimeout)
	reachable := s.client.Reachable(probeCtx)
	cancel()
	if !reachable {
		return
	}

	pairs, broken, err := scanBlackholeDir(s.dir)
	if err != nil {
		logger.Error(fmt.Sprintf("blackhole sweep: read dir %s: %v", s.dir, err))
		return
	}
	for _, b := range broken {
		// битая пара незаконченной замены все еще метка: снять ее значило бы
		// оставить новый торрент остановленным навсегда
		if replacePair(b.opts, b.hash) && !strings.HasPrefix(b.reason, reasonHashMismatch) {
			if time.Since(b.modTime) >= freshPairGrace {
				s.finishReplace(ctx, blackholePair{hash: b.hash, opts: b.opts, modTime: b.modTime}, prevAbsent)
			}
			continue
		}
		// битая запись опасна, только пока нет торрента, который она защищает. Если он уже в клиенте,
		// запись можно убрать, как с ErrAlreadyAdded для целой пары ниже. Кроме hash mismatch: там
		// байты от другого infohash, и торрент из имени файла ничего про них не говорит
		present, err := s.client.Exists(ctx, b.hash)
		if err == nil && present && !strings.HasPrefix(b.reason, reasonHashMismatch) {
			removeBlackholePair(s.dir, b.hash)
			logger.Info(fmt.Sprintf("blackhole sweep: removed broken pair %s (%s): torrent already present in client", b.hash, b.reason))
			continue
		}
		logger.Warn(fmt.Sprintf("blackhole sweep: skipping broken pair %s: %s", b.hash, b.reason))
	}

	for _, p := range pairs {
		if ctx.Err() != nil {
			return
		}
		if age := time.Since(p.modTime); age < freshPairGrace {
			logger.Debug(fmt.Sprintf("blackhole sweep: skipping fresh pair %s (%s), age %s", p.opts.Name, p.hash, age.Round(time.Second)))
			continue
		}
		if replacePair(p.opts, p.hash) {
			s.finishReplace(ctx, p, prevAbsent)
			continue
		}
		addErr := s.readd(ctx, p)
		switch {
		case addErr == nil:
			if s.Settled(ctx, p.hash) {
				logger.Info(fmt.Sprintf("blackhole sweep: re-added %s (%s)", p.opts.Name, p.hash))
				removeBlackholePair(s.dir, p.hash)
			}
		case errors.Is(addErr, model.ErrAlreadyAdded):
			if s.Settled(ctx, p.hash) {
				logger.Info(fmt.Sprintf("blackhole sweep: %s (%s) already added", p.opts.Name, p.hash))
				removeBlackholePair(s.dir, p.hash)
			}
		default:
			logger.Warn(fmt.Sprintf("blackhole sweep: re-add %s (%s) failed: %v", p.opts.Name, p.hash, addErr))
		}
	}
}

func (s *Store) readd(ctx context.Context, p blackholePair) error {
	addCtx, cancel := context.WithTimeout(ctx, AddTimeout)
	defer cancel()
	opts := p.opts
	// новый торрент замены всегда запускаем. Остановленную обычную пару пишут перезаливка
	// с комментарием и «Перекачать» для торрента, который пользователь остановил сам
	if replacePair(p.opts, p.hash) {
		opts.Stopped = false
	}
	if len(p.torrent) > 0 {
		return s.client.AddFromBuffer(addCtx, bytes.NewBuffer(p.torrent), opts)
	}
	return s.client.AddFromMagnet(addCtx, p.magnet, opts)
}

func (s *Store) finishReplace(ctx context.Context, p blackholePair, prevAbsent map[string]time.Time) {
	old := p.opts.ReplacesHash
	oldPresent, err := s.client.Exists(ctx, old)
	if err != nil {
		logger.Warn(fmt.Sprintf("blackhole sweep: replace %s (%s): old %s presence check failed: %v, keeping pair", p.opts.Name, p.hash, old, err))
		return
	}
	if oldPresent {
		// старый жив, значит его файлы целы: убираем новый без файлов, и
		// следующий проход runner повторит замену с начала
		newPresent, err := s.client.Exists(ctx, p.hash)
		if err != nil {
			logger.Warn(fmt.Sprintf("blackhole sweep: replace %s (%s): presence check failed: %v, keeping pair", p.opts.Name, p.hash, err))
			return
		}
		if newPresent {
			if err = s.client.Discard(ctx, p.hash); err != nil {
				logger.Warn(fmt.Sprintf("blackhole sweep: replace %s (%s): discard failed: %v, keeping pair", p.opts.Name, p.hash, err))
				return
			}
		}
		removeBlackholePair(s.dir, p.hash)
		logger.Info(fmt.Sprintf("blackhole sweep: replace %s (%s) rolled back, old torrent %s kept", p.opts.Name, p.hash, old))
		return
	}
	first, seen := prevAbsent[p.hash]
	if !seen {
		s.absentSeen[p.hash] = time.Now()
		logger.Info(fmt.Sprintf("blackhole sweep: replace %s (%s): old torrent absent, confirming on the next sweep", p.opts.Name, p.hash))
		return
	}
	if time.Since(first) < confirmAbsentAfter {
		s.absentSeen[p.hash] = first
		return
	}
	// одной пробе "нет" не верим: клиент мог еще не дочитать свой список
	if err = sleepCtx(ctx, SettleDelay); err != nil {
		return
	}
	if oldPresent, err = s.client.Exists(ctx, old); err != nil || oldPresent {
		logger.Warn(fmt.Sprintf("blackhole sweep: replace %s (%s): old %s not confirmed absent (present %v, err %v), keeping pair", p.opts.Name, p.hash, old, oldPresent, err))
		return
	}
	newPresent, err := s.client.Exists(ctx, p.hash)
	if err != nil {
		logger.Warn(fmt.Sprintf("blackhole sweep: replace %s (%s): presence check failed: %v, keeping pair", p.opts.Name, p.hash, err))
		return
	}
	if newPresent {
		resumeCtx, cancel := context.WithTimeout(ctx, AddTimeout)
		err = s.client.Resume(resumeCtx, p.hash)
		cancel()
		if err != nil {
			logger.Warn(fmt.Sprintf("blackhole sweep: replace %s (%s): start failed: %v, keeping pair", p.opts.Name, p.hash, err))
			return
		}
	} else if len(p.torrent) == 0 && p.magnet == "" {
		logger.Warn(fmt.Sprintf("blackhole sweep: replace %s (%s): broken pair, both torrents absent, nothing to re-add, keeping pair", p.opts.Name, p.hash))
		return
	} else if err = s.readd(ctx, p); err != nil && !errors.Is(err, model.ErrAlreadyAdded) {
		logger.Warn(fmt.Sprintf("blackhole sweep: replace %s (%s): re-add failed: %v, keeping pair", p.opts.Name, p.hash, err))
		return
	}
	if s.Settled(ctx, p.hash) {
		logger.Info(fmt.Sprintf("blackhole sweep: replace %s (%s) finished", p.opts.Name, p.hash))
		removeBlackholePair(s.dir, p.hash)
	}
}

func (s *Store) PendingReplace(hash string) bool {
	if s.dir == "" {
		return false
	}
	_, jsonPath := blackholePaths(s.dir, hash)
	if _, err := os.Stat(jsonPath); err != nil {
		// и при иной ошибке (каталог недоступен) пары нет: записать ее туда
		// нельзя, так что до удаления файлов дело не дойдет
		if !errors.Is(err, os.ErrNotExist) {
			logger.Warn(fmt.Sprintf("blackhole: pending replace check %s: %v", hash, err))
		}
		return false
	}
	// нечитаемую пару считаем незаконченной заменой: ошибка в эту сторону только
	// отложит обновление, но не потеряет его
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return true
	}
	var meta blackholeMeta
	if err = json.Unmarshal(raw, &meta); err != nil {
		return true
	}
	return meta.ReplacesHash != ""
}

func replacePair(opts model.ManagerAddFileOptions, hash string) bool {
	return opts.ReplacesHash != "" && !strings.EqualFold(opts.ReplacesHash, hash)
}
