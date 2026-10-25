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

var presencePoll = time.Second

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
		case <-time.After(presencePoll):
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
		case <-time.After(presencePoll):
		}
	}
	return fmt.Errorf("torrent add failed (not present after add), qbittorrent rejected it (possibly duplicate or invalid)")
}

func (c *Client) addTorrentVerified(ctx context.Context, data []byte, opts model.ManagerAddFileOptions) error {
	hash, err := torrentmeta.InfoHash(data)
	if err != nil {
		return err
	}
	if err = refuseV2(data, opts.Name); err != nil {
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
	if err = c.ensurePresentAfterAdd(ctx, hash); err != nil {
		return err
	}
	if len(opts.FilePaths) > 0 {
		c.restorePaths(ctx, hash, opts)
	}
	if len(opts.FileSelection) == 0 {
		return nil
	}
	return c.applySelection(ctx, hash, opts)
}

func (c *Client) addMagnetURLVerified(ctx context.Context, magnet string, opts model.ManagerAddFileOptions) error {
	// btmh есть только у v2/hybrid, а их qbit ищет по SHA-256 и проверка присутствия по btih промахнулась бы
	if strings.Contains(strings.ToLower(magnet), "xt=urn:btmh:") {
		return errV2(opts.Name)
	}
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

func refuseV2(data []byte, name string) error {
	// UnmarshalInfo строже InfoHash: при ошибке разбора ведем себя как раньше, иначе перестал бы добавляться годный v1
	if v2, err := torrentmeta.HasV2(data); err == nil && v2 {
		return errV2(name)
	}
	return nil
}

func errV2(name string) error {
	return fmt.Errorf("torrent %s has v2 metadata: qBittorrent keys it by SHA-256, not supported", name)
}
