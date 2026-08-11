package qbittorrent

import (
	"context"
	"fmt"
	"strings"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"trackerr/internal/helper/torrentmeta"
	"trackerr/internal/model"
)

func (c *client) torrentExists(ctx context.Context, hash string) (bool, error) {
	torrents, err := c.client.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{Hashes: []string{hash}})
	if err != nil {
		return false, err
	}
	return len(torrents) > 0, nil
}

func (c *client) ensureNotPresent(ctx context.Context, hash, name string) error {
	exists, err := c.torrentExists(ctx, hash)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: %s", model.ErrAlreadyAdded, name)
	}
	return nil
}

func (c *client) waitUntilAbsent(ctx context.Context, hash string) (bool, error) {
	for i := 0; i < 5; i++ {
		exists, err := c.torrentExists(ctx, hash)
		if err != nil {
			return false, err
		}
		if !exists {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return false, nil
}

func (c *client) preAddPresenceCheck(ctx context.Context, hash, name, optsHash string) error {
	if strings.EqualFold(hash, optsHash) && c.config.DeleteEnabled {
		absent, err := c.waitUntilAbsent(ctx, hash)
		if err != nil {
			return err
		}
		if !absent {
			return fmt.Errorf("%w: %s", model.ErrAlreadyAdded, name)
		}
		return nil
	}
	return c.ensureNotPresent(ctx, hash, name)
}

func (c *client) ensurePresentAfterAdd(ctx context.Context, hash string) error {
	for i := 0; i < 5; i++ {
		exists, err := c.torrentExists(ctx, hash)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("torrent add failed (not present after add), qbittorrent rejected it (possibly duplicate or invalid)")
}

func (c *client) addTorrentVerified(ctx context.Context, data []byte, opts model.ManagerAddFileOptions) error {
	hash, err := torrentmeta.InfoHash(data)
	if err != nil {
		return err
	}
	if err = c.preAddPresenceCheck(ctx, hash, opts.Name, opts.Hash); err != nil {
		return err
	}
	if err = c.client.AddTorrentFromMemoryCtx(ctx, data, torrentAddOptions(opts).Prepare()); err != nil {
		return err
	}
	return c.ensurePresentAfterAdd(ctx, hash)
}

func (c *client) addMagnetURLVerified(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error {
	hash, ok := torrentmeta.BtihFromMagnet(magnet)
	if ok {
		if err := c.preAddPresenceCheck(ctx, hash, opts.Name, opts.Hash); err != nil {
			return err
		}
	}
	if err := c.client.AddTorrentFromUrlCtx(ctx, magnet, torrentAddOptions(opts).Prepare()); err != nil {
		return err
	}
	if ok {
		return c.ensurePresentAfterAdd(ctx, hash)
	}
	return nil
}
