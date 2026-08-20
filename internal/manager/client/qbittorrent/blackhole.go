package qbittorrent

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

	qbt "github.com/autobrr/go-qbittorrent"

	"trackerr/internal/helper/torrentmeta"
	"trackerr/internal/model"
	"trackerr/internal/system/logger"
)

const (
	blackholeSweepInterval = 5 * time.Minute
	// blackholeReachableTimeout ограничивает пробу доступности: без неё
	// зависший qbit навсегда блокирует горутину подметания, а app.Run ждёт
	// эту горутину перед возвратом, так что зависает и перезагрузка.
	blackholeReachableTimeout = 30 * time.Second
	// blackholeAddTimeout это страховка от зависшего HTTP-вызова, а не
	// регулятор очереди: он должен оставаться длиннее metadataFetchTimeout,
	// иначе у магнита с заданным opts.Comment (addMagnetWithComment) контекст
	// убьют посреди waitForMetadata, ошибка вернётся как
	// context.DeadlineExceeded (а не errFetchMetadataUnsupported или
	// errMetadataFetchTimeout), запасной путь без комментария не сработает,
	// а если бы и сработал, то с тем же уже истёкшим контекстом.
	// Медленный рой тогда не переподхватится никогда, по минуте впустую на
	// каждый тик. Задержка всего, что стоит за ним в очереди того же тика,
	// принимается как плата.
	blackholeAddTimeout = metadataFetchTimeout + time.Minute
)

func blackholePaths(dir, hash string) (torrentPath, jsonPath string) {
	hash = strings.ToLower(hash)
	return filepath.Join(dir, hash+".torrent"), filepath.Join(dir, hash+".json")
}

type blackholeMeta struct {
	model.ManagerAddFileOptions
	Magnet string
}

func (c *Client) writeBlackholePair(data []byte, magnet string, opts model.ManagerAddFileOptions) error {
	if c.config.BlackholeDir == "" {
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
	torrentPath, jsonPath := blackholePaths(c.config.BlackholeDir, opts.Hash)
	wroteTorrent := false
	if len(data) > 0 {
		if err = os.WriteFile(torrentPath, data, 0o644); err != nil {
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
	// для пары только с магнитом json единственная копия страховки, поэтому
	// os.WriteFile с усечением и записью теряет её при падении посреди записи;
	// вместо этого пишем в соседний временный файл, fsync и переименование поверх цели
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err = f.Sync(); err != nil {
		// fsync поддерживается не на всякой файловой системе; не валим из-за
		// него всю запись, просто отмечаем (переименованию ниже для
		// надёжности нужен свой fsync каталога, он делается отдельно)
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
	// само переименование не надёжно, пока не сброшена запись каталога:
	// без этого падение сразу после Rename на некоторых файловых системах
	// всё ещё теряет файл, хотя Rename вернул успех. История та же, что с
	// fsync файла выше: не фатально, поддерживается не везде.
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

func (c *Client) WriteBlackholePair(data []byte, magnet string, opts model.ManagerAddFileOptions) error {
	return c.writeBlackholePair(data, magnet, opts)
}

func (c *Client) RemoveBlackholePair(hash string) {
	removeBlackholePair(c.config.BlackholeDir, hash)
}

func (c *Client) ConfirmSettled(ctx context.Context, hash string) bool {
	return c.blackholeSettled(ctx, hash)
}

type blackholePair struct {
	hash    string
	torrent []byte
	magnet  string
	opts    model.ManagerAddFileOptions
}

const reasonHashMismatch = "hash mismatch"

type blackholeBroken struct {
	hash   string
	reason string
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
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// файлы с любым другим расширением не трогаем вообще, даже не логируем
		switch ext := filepath.Ext(name); ext {
		case ".torrent":
			// приводим к нижнему регистру, чтобы хеш совпал с тем, что
			// пересобирает removeBlackholePair (blackholePaths всегда понижает
			// регистр), иначе пара со смешанным регистром на диске оживала бы
			// каждый тик вечно и её нельзя было бы удалить
			torrents[strings.ToLower(strings.TrimSuffix(name, ext))] = filepath.Join(dir, name)
		case ".json":
			jsons[strings.ToLower(strings.TrimSuffix(name, ext))] = filepath.Join(dir, name)
		}
	}

	for hash, jPath := range jsons {
		tPath, hasTorrent := torrents[hash]
		data, magnet, opts, reason := loadBlackholePair(hash, jPath, tPath, hasTorrent)
		if reason != "" {
			broken = append(broken, blackholeBroken{hash: hash, reason: reason})
			continue
		}
		pairs = append(pairs, blackholePair{hash: hash, torrent: data, magnet: magnet, opts: opts})
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
	// meta встраивает ManagerAddFileOptions плоско, поэтому .json, записанный
	// старым кодом (только опции, без ключа Magnet), тоже разбирается здесь,
	// а Magnet остаётся нулевым: миграция существующих пар не нужна.
	opts = meta.ManagerAddFileOptions
	// тело json "null" или "{}" разбирается без ошибки и с нулевым opts:
	// ловим это здесь, а не восстанавливаем торрент без категории/тегов/имени
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
		// битый .torrent рядом с рабочим магнитом ещё восстановим, не даём ему
		// вечно гнить сломанным, но ошибку чтения оставляем на виду: это
		// единственный сигнал умирающего диска до того, как сломаются все пары
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
	// непустой .torrent всё ещё может быть обрезанным bencode-блобом после
	// падения посреди записи; запасной магнит означает, что AddFromBuffer не
	// получит байты, на которых torrentmeta.InfoHash снова просто упадёт
	if _, err = torrentmeta.InfoHash(data); err != nil {
		if meta.Magnet != "" {
			logger.Warn(fmt.Sprintf("blackhole %s: parse torrent: %v, falling back to magnet", hash, err))
			return nil, meta.Magnet, opts, ""
		}
		return nil, "", opts, fmt.Sprintf("parse torrent: %v", err)
	}
	return data, meta.Magnet, opts, ""
}

func (c *Client) SweepBlackhole(ctx context.Context) {
	if c.config.BlackholeDir == "" {
		return
	}
	c.sweepBlackholeOnce(ctx)
	ticker := time.NewTicker(blackholeSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.sweepBlackholeOnce(ctx)
		}
	}
}

func (c *Client) blackholeReachable(ctx context.Context) bool {
	probeCtx, cancel := context.WithTimeout(ctx, blackholeReachableTimeout)
	defer cancel()
	_, err := c.client.GetTorrentsCtx(probeCtx, qbt.TorrentFilterOptions{})
	return err == nil
}

func (c *Client) blackholeSettled(ctx context.Context, hash string) bool {
	// задача qbit на удаление с файлами идёт асинхронно: торрент исчезает из
	// torrents/list в момент вызова удаления, но удаление файлов продолжается
	// после этого. Успешное добавление заново (или ErrAlreadyAdded) значит лишь,
	// что торрент в списке прямо сейчас, а не что никакая задача удаления не
	// гонится снести его вместе с файлами снова. Выжидаем ту же задержку, на
	// которую опирается ForceRedownload, прежде чем верить в присутствие, и
	// берём для этого не ctx конкретного добавления, тот уже отменён вызывающим.
	if err := sleepCtx(ctx, redownloadSettleDelay); err != nil {
		logger.Warn(fmt.Sprintf("blackhole: %s settle wait interrupted: %v, keeping record for a later retry", hash, err))
		return false
	}
	present, err := c.torrentExists(ctx, hash)
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

func (c *Client) sweepBlackholeOnce(ctx context.Context) {
	// одна проба доступности на тик: нет смысла обходить каталог (и логировать
	// сломанные пары каждый тик), пока qbit просто лежит
	if !c.blackholeReachable(ctx) {
		return
	}

	pairs, broken, err := scanBlackholeDir(c.config.BlackholeDir)
	if err != nil {
		logger.Error(fmt.Sprintf("blackhole sweep: read dir %s: %v", c.config.BlackholeDir, err))
		return
	}
	for _, b := range broken {
		// сломанная запись опасна только пока отсутствует торрент, который она
		// должна была защитить; если торрент уже в клиенте, запись ничего не
		// защищает и может уйти, тот же вызов ErrAlreadyAdded уже делает это
		// для валидной пары ниже
		// кроме случая несовпадения хеша: там байты на диске принадлежат
		// другому infohash, чем имя файла, поэтому присутствие торрента из
		// имени файла ничего не говорит о том, что защищает эта запись
		present, err := c.torrentExists(ctx, b.hash)
		if err == nil && present && !strings.HasPrefix(b.reason, reasonHashMismatch) {
			removeBlackholePair(c.config.BlackholeDir, b.hash)
			logger.Info(fmt.Sprintf("blackhole sweep: removed broken pair %s (%s): torrent already present in client", b.hash, b.reason))
			continue
		}
		logger.Warn(fmt.Sprintf("blackhole sweep: skipping broken pair %s: %s", b.hash, b.reason))
	}

	for _, p := range pairs {
		if ctx.Err() != nil {
			return
		}
		addCtx, cancel := context.WithTimeout(ctx, blackholeAddTimeout)
		var addErr error
		if len(p.torrent) > 0 {
			addErr = c.AddFromBuffer(addCtx, bytes.NewBuffer(p.torrent), p.opts)
		} else {
			addErr = c.AddFromMagnet(addCtx, p.magnet, p.opts)
		}
		cancel()
		switch {
		case addErr == nil:
			if c.blackholeSettled(ctx, p.hash) {
				logger.Info(fmt.Sprintf("blackhole sweep: re-added %s (%s)", p.opts.Name, p.hash))
				removeBlackholePair(c.config.BlackholeDir, p.hash)
			}
		case errors.Is(addErr, model.ErrAlreadyAdded):
			if c.blackholeSettled(ctx, p.hash) {
				logger.Info(fmt.Sprintf("blackhole sweep: %s (%s) already added", p.opts.Name, p.hash))
				removeBlackholePair(c.config.BlackholeDir, p.hash)
			}
		default:
			logger.Warn(fmt.Sprintf("blackhole sweep: re-add %s (%s) failed: %v", p.opts.Name, p.hash, addErr))
		}
	}
}
