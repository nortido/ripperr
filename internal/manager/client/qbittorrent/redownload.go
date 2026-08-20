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
	// redownloadAddAttempts is how many times the re-add is tried before failing.
	redownloadAddAttempts = 3
)

var (
	// redownloadSettleDelay gives qBittorrent time to finish deleting files: the
	// torrent leaves the list right away, but the removal job keeps running and
	// re-adding the same infohash too early is silently rejected.
	redownloadSettleDelay = 3 * time.Second
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

	// Step 4: blackhole the bytes and the add parameters before anything
	// destructive happens. If qbit dies or the request is canceled anywhere
	// after this point, the pair on disk is enough to finish the re-add later.
	if err = c.writeBlackholePair(torrentData, "", opts); err != nil {
		return fmt.Errorf("blackhole write failed: %w", err)
	}

	// Step 5: Delete torrent with files, bypassing settings
	logger.Info(fmt.Sprintf("force redownload: deleting torrent %s with files", torrentHash))
	if err = c.client.DeleteTorrentsCtx(ctx, []string{torrentHash}, true); err != nil {
		return fmt.Errorf("delete torrent failed: %w", err)
	}

	// Step 6: Wait until torrent is gone
	logger.Info(fmt.Sprintf("force redownload: waiting for torrent %s to be deleted", torrentHash))
	absent, err := c.waitUntilAbsent(ctx, torrentHash)
	if err != nil {
		return fmt.Errorf("wait until absent failed: %w", err)
	}
	if !absent {
		return fmt.Errorf("torrent did not disappear after deletion")
	}

	// Step 7: Let the removal job finish on disk, then confirm the torrent is
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

	// Step 8: Add torrent back from exported bytes, retrying a silent rejection
	logger.Info(fmt.Sprintf("force redownload: re-adding torrent %s", torrentHash))
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
			// leave the blackhole pair on disk: the sweeper picks it up later
			return err
		}
	}
	if addErr != nil {
		// leave the blackhole pair on disk: the sweeper picks it up later
		return fmt.Errorf("add torrent failed: %w", addErr)
	}

	// the add itself succeeded, but qbit's own delete-with-files job from step 5
	// can still be racing to take the freshly re-added torrent back down; only
	// drop the safety net once presence survives the same settle wait the
	// sweeper trusts, otherwise leave it for the sweeper to sort out
	if !c.blackholeSettled(ctx, torrentHash) {
		logger.Info(fmt.Sprintf("force redownload: %s not confirmed settled, leaving blackhole pair for the sweeper", torrentHash))
		return nil
	}

	removeBlackholePair(c.config.BlackholeDir, torrentHash)
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
