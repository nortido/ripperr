package qbittorrent

import (
	"context"
	"errors"
	"fmt"

	"trackerr/internal/model"
	"trackerr/internal/system/logger"
)

func (c *client) ForceRedownload(ctx context.Context, torrentHash string) error {
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

	// Step 6: Add torrent back from exported bytes
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
	if err = c.addTorrentVerified(ctx, torrentData, opts); err != nil {
		return fmt.Errorf("add torrent failed: %w", err)
	}

	logger.Info(fmt.Sprintf("force redownload: completed for torrent %s", torrentHash))
	return nil
}
