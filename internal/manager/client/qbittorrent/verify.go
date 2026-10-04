package qbittorrent

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
)

func (c *Client) IncomingHashSameAs(buf *bytes.Buffer, magnet string, existing string) (same bool, known bool) {
	var data []byte
	if buf != nil {
		data = buf.Bytes()
	}
	return torrentmeta.SameHash(data, magnet, existing)
}

func (c *Client) torrentExists(ctx context.Context, hash string) (bool, error) {
	torrents, err := c.client.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{Hashes: []string{hash}})
	if err != nil {
		return false, err
	}
	return len(torrents) > 0, nil
}

func (c *Client) ensureNotPresent(ctx context.Context, hash, name string) error {
	exists, err := c.torrentExists(ctx, hash)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: %s", model.ErrAlreadyAdded, name)
	}
	return nil
}

func (c *Client) WaitAbsent(ctx context.Context, hash string) (bool, error) {
	return c.waitUntilAbsent(ctx, hash)
}

func (c *Client) waitUntilAbsent(ctx context.Context, hash string) (bool, error) {
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

func (c *Client) preAddPresenceCheck(ctx context.Context, hash, name, optsHash string) error {
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

func (c *Client) ensurePresentAfterAdd(ctx context.Context, hash string) error {
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

func (c *Client) addTorrentVerified(ctx context.Context, data []byte, opts model.ManagerAddFileOptions) error {
	hash, err := torrentmeta.InfoHash(data)
	if err != nil {
		return err
	}
	if err = c.preAddPresenceCheck(ctx, hash, opts.Name, opts.Hash); err != nil {
		return err
	}
	addOpts := opts
	addOpts.Stopped = opts.Stopped || len(opts.FileSelection) > 0
	if err = c.client.AddTorrentFromMemoryCtx(ctx, data, addOptions(addOpts)); err != nil {
		return err
	}
	if err = c.ensurePresentAfterAdd(ctx, hash); err != nil || len(opts.FileSelection) == 0 {
		return err
	}
	return c.applySelection(ctx, hash, opts)
}

func (c *Client) addMagnetURLVerified(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error {
	hash, ok := torrentmeta.BtihFromMagnet(magnet)
	if ok {
		if err := c.preAddPresenceCheck(ctx, hash, opts.Name, opts.Hash); err != nil {
			return err
		}
	}
	if err := c.client.AddTorrentFromUrlCtx(ctx, magnet, addOptions(opts)); err != nil {
		return err
	}
	if ok {
		return c.ensurePresentAfterAdd(ctx, hash)
	}
	return nil
}
