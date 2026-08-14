package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"trackerr/internal/model"
	"trackerr/internal/system/logger"
)

const (
	// redownloadSettleDelay gives qBittorrent time to finish deleting files: the
	// torrent leaves the list right away, but the removal job keeps running and
	// re-adding the same infohash too early is silently rejected.
	redownloadSettleDelay = 3 * time.Second
	// redownloadAddAttempts is how many times the re-add is tried before failing.
	redownloadAddAttempts = 3
	// redownloadRetryDelay is the pause between re-add attempts.
	redownloadRetryDelay = 5 * time.Second
)

func (c *Client) ForceRedownload(ctx context.Context, torrentHash string) error {
	logger.Info(fmt.Sprintf("force redownload: exporting torrent %s", torrentHash))

	// Step 1: Export torrent to memory
	torrentData, err := c.client.ExportTorrentCtx(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("export torrent failed: %w", err)
	}
	if len(torrentData) == 0 {
		return fmt.Errorf("export torrent failed: empty result")
	}

	// Step 2: Get torrent name and comment
	name, comment, err := c.GetTorrentDetails(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("get torrent details failed: %w", err)
	}

	// Step 3: Get category, tag and other parameters
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

	// Step 4: Delete torrent with files, bypassing settings
	logger.Info(fmt.Sprintf("force redownload: deleting torrent %s with files", torrentHash))
	if err = c.client.DeleteTorrentsCtx(ctx, []string{torrentHash}, true); err != nil {
		return fmt.Errorf("delete torrent failed: %w", err)
	}

	// Step 5: Wait until torrent is gone
	logger.Info(fmt.Sprintf("force redownload: waiting for torrent %s to be deleted", torrentHash))
	absent, err := c.waitUntilAbsent(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("wait until absent failed: %w", err)
	}
	if !absent {
		return fmt.Errorf("torrent did not disappear after deletion")
	}

	// Step 6: Let the removal job finish on disk, then confirm the torrent is
	// really gone before reusing its infohash.
	if err = sleepCtx(ctx, redownloadSettleDelay); err != nil {
		return err
	}
	present, err := c.torrentExists(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("presence check failed: %w", err)
	}
	if present {
		return fmt.Errorf("torrent reappeared after deletion")
	}

	// Step 7: Add torrent back from exported bytes, retrying a silent rejection
	logger.Info(fmt.Sprintf("force redownload: re-adding torrent %s", torrentHash))
	opts := model.ManagerAddFileOptions{
		Hash:           torrentHash,
		Name:           name,
		Comment:        comment,
		Category:       entry.Category,
		Tag:            entry.Tag,
		AutoManaged:    entry.AutoManaged,
		MaxRatio:       entry.MaxRatio,
		MaxSeedingTime: entry.MaxSeedingTime,
	}
	var addErr error
	for attempt := 1; attempt <= redownloadAddAttempts; attempt++ {
		addErr = c.addTorrentVerified(ctx, torrentData, opts)
		if addErr == nil {
			break
		}
		if errors.Is(addErr, model.ErrAlreadyAdded) {
			// A previous attempt landed after our check: the torrent is back.
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
			return err
		}
	}
	if addErr != nil {
		return fmt.Errorf("add torrent failed: %w", addErr)
	}

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
