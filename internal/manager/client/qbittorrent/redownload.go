package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/manager/blackhole"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

const (
	redownloadAddAttempts = 3
)

var (
	redownloadRetryDelay = 5 * time.Second
)

func (c *Client) ForceRedownload(ctx context.Context, torrentHash string, ro model.RedownloadOptions) error {
	logger.Info(fmt.Sprintf("force redownload: exporting torrent %s", torrentHash))

	torrentData, err := c.client.ExportTorrentCtx(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("export torrent failed: %w", err)
	}
	if len(torrentData) == 0 {
		return fmt.Errorf("export torrent failed: empty result")
	}
	// удаление с файлами без возможности добавить обратно потеряло бы раздачу, поэтому отказ до любых изменений
	if err = refuseV2(torrentData, torrentHash); err != nil {
		return err
	}

	name, comment, err := c.GetTorrentDetails(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("get torrent details failed: %w", err)
	}
	if ro.Comment != "" {
		// qbit берет комментарий только из .torrent, add его не задает. Правим до записи пары и удаления:
		// подметальщик должен добавить тот же файл, а сбой правки ничего не ломает
		if torrentData, err = torrentmeta.SetComment(torrentData, ro.Comment); err != nil {
			return fmt.Errorf("set comment failed: %w", err)
		}
		comment = ro.Comment
	}

	entries, err := c.List(ctx, model.FilterOptions{})
	if err != nil && !errors.Is(err, model.ErrPartialList) {
		return fmt.Errorf("list torrents failed: %w", err)
	}

	var entry *model.Entry
	for _, e := range entries {
		if e.Hash == torrentHash {
			entry = e
			break
		}
	}
	if entry == nil {
		return fmt.Errorf("torrent not found in list")
	}

	opts := model.ManagerAddFileOptions{
		Hash:           torrentHash,
		Name:           name,
		Comment:        comment,
		Category:       entry.Category,
		SavePath:       entry.SavePath,
		Tag:            entry.Tag,
		AutoManaged:    entry.AutoManaged,
		MaxRatio:       entry.MaxRatio,
		MaxSeedingTime: entry.MaxSeedingTime,
		Stopped:        entry.Stopped,
	}
	files, err := c.Files(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("read file selection: %w", err)
	}
	if slices.ContainsFunc(files, func(f model.TorrentFile) bool { return !f.Wanted }) {
		opts.FileSelection = files
	}
	opts.FilePaths = renamedPaths(torrentData, files)

	// пара ложится в blackhole до удаления: если qbit упадет или запрос отменят дальше,
	// подметальщик сам доведет повторное добавление
	if err = c.bh.Write(torrentData, "", opts); err != nil {
		return fmt.Errorf("blackhole write failed: %w", err)
	}

	logger.Info(fmt.Sprintf("force redownload: deleting torrent %s with files", torrentHash))
	if err = c.client.DeleteTorrentsCtx(ctx, []string{torrentHash}, true); err != nil {
		return fmt.Errorf("delete torrent failed: %w", err)
	}

	logger.Info(fmt.Sprintf("force redownload: waiting for torrent %s to be deleted", torrentHash))
	absent, err := c.waitUntilAbsent(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("wait until absent failed: %w", err)
	}
	if !absent {
		return fmt.Errorf("torrent did not disappear after deletion")
	}

	// даем удалению доработать на диске и еще раз проверяем, что торрента нет.
	// Только потом его infohash можно занимать снова
	if err = sleepCtx(ctx, blackhole.SettleDelay); err != nil {
		return err
	}
	present, err := c.torrentExists(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("presence check failed: %w", err)
	}
	if present {
		return fmt.Errorf("torrent reappeared after deletion")
	}

	// qbit иногда молча не добавляет, поэтому несколько попыток
	logger.Info(fmt.Sprintf("force redownload: re-adding torrent %s", torrentHash))
	var addErr error
	for attempt := 1; attempt <= redownloadAddAttempts; attempt++ {
		addErr = c.addTorrentVerified(ctx, torrentData, opts)
		if addErr == nil {
			break
		}
		if errors.Is(addErr, model.ErrAlreadyAdded) {
			// прошлая попытка все-таки дошла, просто позже проверки. Выбор файлов, запуск и имена
			// после нее не ставились: доводим
			logger.Info(fmt.Sprintf("force redownload: torrent %s is already back, converging", torrentHash))
			if cerr := c.ConvergeAdded(ctx, torrentHash, torrentData, opts); cerr != nil {
				// торрент уже снова в qbit, перекачка удалась. Пару не убираем, выбор и имена доведет подметальщик
				logger.Warn(fmt.Sprintf("force redownload: converge %s (%s) failed: %v, leaving blackhole pair for the sweeper", name, torrentHash, cerr))
				return nil
			}
			addErr = nil
			break
		}
		if attempt == redownloadAddAttempts {
			break
		}
		logger.Warn(fmt.Sprintf("force redownload: add attempt %d/%d for %s failed: %v",
			attempt, redownloadAddAttempts, torrentHash, addErr))
		if err = sleepCtx(ctx, redownloadRetryDelay); err != nil {
			// пару в blackhole не трогаем, ее подберет подметальщик
			return err
		}
	}
	if addErr != nil {
		// пара остается подметальщику
		return fmt.Errorf("add torrent failed: %w", addErr)
	}

	// удаление с файлами в qbit еще может догнать новый торрент: пару убираем, только если он
	// пережил ту же задержку, что ждет подметальщик, иначе пусть он и разбирается
	if !c.bh.Settled(ctx, torrentHash) {
		logger.Info(fmt.Sprintf("force redownload: %s not confirmed settled, leaving blackhole pair for the sweeper", torrentHash))
		return nil
	}

	c.bh.Remove(torrentHash)
	logger.Info(fmt.Sprintf("force redownload: completed for torrent %s", torrentHash))
	return nil
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
