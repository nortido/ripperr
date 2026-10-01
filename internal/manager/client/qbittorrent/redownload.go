package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ripperr/internal/manager/blackhole"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

const (
	// redownloadAddAttempts это сколько раз пробуем добавить заново до отказа.
	redownloadAddAttempts = 3
)

var (
	// redownloadRetryDelay это пауза между попытками добавить заново.
	redownloadRetryDelay = 5 * time.Second
)

func (c *Client) ForceRedownload(ctx context.Context, torrentHash string) error {
	logger.Info(fmt.Sprintf("force redownload: exporting torrent %s", torrentHash))

	// Шаг 1: выгружаем торрент в память
	torrentData, err := c.client.ExportTorrentCtx(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("export torrent failed: %w", err)
	}
	if len(torrentData) == 0 {
		return fmt.Errorf("export torrent failed: empty result")
	}

	// Шаг 2: берём имя и комментарий торрента
	name, comment, err := c.GetTorrentDetails(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("get torrent details failed: %w", err)
	}

	// Шаг 3: берём категорию, тег и прочие параметры
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
	}

	// Шаг 4: складываем байты и параметры добавления в blackhole до всего
	// разрушительного. Если qbit умрёт или запрос отменят где-то дальше этой
	// точки, пары на диске хватит, чтобы дозавершить повторное добавление позже.
	if err = c.bh.Write(torrentData, "", opts); err != nil {
		return fmt.Errorf("blackhole write failed: %w", err)
	}

	// Шаг 5: удаляем торрент вместе с файлами в обход настроек
	logger.Info(fmt.Sprintf("force redownload: deleting torrent %s with files", torrentHash))
	if err = c.client.DeleteTorrentsCtx(ctx, []string{torrentHash}, true); err != nil {
		return fmt.Errorf("delete torrent failed: %w", err)
	}

	// Шаг 6: ждём, пока торрент исчезнет
	logger.Info(fmt.Sprintf("force redownload: waiting for torrent %s to be deleted", torrentHash))
	absent, err := c.waitUntilAbsent(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("wait until absent failed: %w", err)
	}
	if !absent {
		return fmt.Errorf("torrent did not disappear after deletion")
	}

	// Шаг 7: даём задаче удаления доработать на диске, потом убеждаемся, что
	// торрент действительно исчез, прежде чем переиспользовать его infohash.
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

	// Шаг 8: добавляем торрент обратно из выгруженных байтов, повторяя при молчаливом отказе
	logger.Info(fmt.Sprintf("force redownload: re-adding torrent %s", torrentHash))
	var addErr error
	for attempt := 1; attempt <= redownloadAddAttempts; attempt++ {
		addErr = c.addTorrentVerified(ctx, torrentData, opts)
		if addErr == nil {
			break
		}
		if errors.Is(addErr, model.ErrAlreadyAdded) {
			// Предыдущая попытка дошла уже после нашей проверки: торрент вернулся.
			logger.Info(fmt.Sprintf("force redownload: torrent %s is already back", torrentHash))
			addErr = nil
			break
		}
		if attempt == redownloadAddAttempts {
			break
		}
		logger.Warn(fmt.Sprintf("force redownload: add attempt %d/%d for %s failed: %v",
			attempt, redownloadAddAttempts, torrentHash, addErr))
		if err = sleepCtx(ctx, redownloadRetryDelay); err != nil {
			// оставляем пару blackhole на диске: подметальщик подберёт её позже
			return err
		}
	}
	if addErr != nil {
		// оставляем пару blackhole на диске: подметальщик подберёт её позже
		return fmt.Errorf("add torrent failed: %w", addErr)
	}

	// само добавление удалось, но задача qbit на удаление с файлами из шага 5
	// всё ещё может гнаться снести только что добавленный торрент; убираем
	// страховку лишь когда присутствие переживает ту же задержку, которой
	// доверяет подметальщик, иначе оставляем разбираться ему
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
