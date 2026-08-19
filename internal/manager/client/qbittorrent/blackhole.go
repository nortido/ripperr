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

	"trackerr/internal/model"
	"trackerr/internal/system/logger"
)

const blackholeSweepInterval = 5 * time.Minute

func blackholePaths(dir, hash string) (torrentPath, jsonPath string) {
	hash = strings.ToLower(hash)
	return filepath.Join(dir, hash+".torrent"), filepath.Join(dir, hash+".json")
}

func (c *Client) writeBlackholePair(data []byte, opts model.ManagerAddFileOptions) error {
	if c.config.BlackholeDir == "" {
		return fmt.Errorf("blackhole directory not configured")
	}
	if opts.Hash == "" {
		return fmt.Errorf("blackhole write: missing hash")
	}
	meta, err := json.Marshal(opts)
	if err != nil {
		return fmt.Errorf("marshal blackhole metadata: %w", err)
	}
	torrentPath, jsonPath := blackholePaths(c.config.BlackholeDir, opts.Hash)
	if err = os.WriteFile(torrentPath, data, 0o644); err != nil {
		return fmt.Errorf("write blackhole torrent: %w", err)
	}
	if err = os.WriteFile(jsonPath, meta, 0o644); err != nil {
		// don't leave an orphan .torrent behind a failed write, it would look
		// like a broken pair to the sweeper forever
		_ = os.Remove(torrentPath)
		return fmt.Errorf("write blackhole metadata: %w", err)
	}
	return nil
}

func removeBlackholePair(dir, hash string) {
	torrentPath, jsonPath := blackholePaths(dir, hash)
	_ = os.Remove(torrentPath)
	_ = os.Remove(jsonPath)
}

func (c *Client) WriteBlackholePair(data []byte, opts model.ManagerAddFileOptions) error {
	return c.writeBlackholePair(data, opts)
}

func (c *Client) RemoveBlackholePair(hash string) {
	removeBlackholePair(c.config.BlackholeDir, hash)
}

type blackholePair struct {
	hash    string
	torrent []byte
	opts    model.ManagerAddFileOptions
}

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
		// files with any other extension are left alone entirely, not even logged
		switch ext := filepath.Ext(name); ext {
		case ".torrent":
			torrents[strings.TrimSuffix(name, ext)] = filepath.Join(dir, name)
		case ".json":
			jsons[strings.TrimSuffix(name, ext)] = filepath.Join(dir, name)
		}
	}

	for hash, tPath := range torrents {
		jPath, ok := jsons[hash]
		if !ok {
			broken = append(broken, blackholeBroken{hash: hash, reason: "missing .json"})
			continue
		}
		data, opts, reason := loadBlackholePair(tPath, jPath)
		if reason != "" {
			broken = append(broken, blackholeBroken{hash: hash, reason: reason})
			continue
		}
		pairs = append(pairs, blackholePair{hash: hash, torrent: data, opts: opts})
	}
	for hash := range jsons {
		if _, ok := torrents[hash]; !ok {
			broken = append(broken, blackholeBroken{hash: hash, reason: "missing .torrent"})
		}
	}
	return pairs, broken, nil
}

func loadBlackholePair(torrentPath, jsonPath string) (data []byte, opts model.ManagerAddFileOptions, reason string) {
	data, err := os.ReadFile(torrentPath)
	if err != nil {
		return nil, opts, fmt.Sprintf("read torrent: %v", err)
	}
	if len(data) == 0 {
		return nil, opts, "empty torrent file"
	}
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, opts, fmt.Sprintf("read json: %v", err)
	}
	if len(raw) == 0 {
		return nil, opts, "empty json file"
	}
	if err = json.Unmarshal(raw, &opts); err != nil {
		return nil, opts, fmt.Sprintf("parse json: %v", err)
	}
	return data, opts, ""
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
	_, err := c.client.GetTorrentsCtx(ctx, qbt.TorrentFilterOptions{})
	return err == nil
}

func (c *Client) sweepBlackholeOnce(ctx context.Context) {
	// a single reachability probe per tick: no point walking the directory (and
	// logging broken pairs on every tick) while qbit is simply down
	if !c.blackholeReachable(ctx) {
		return
	}

	pairs, broken, err := scanBlackholeDir(c.config.BlackholeDir)
	if err != nil {
		logger.Error(fmt.Sprintf("blackhole sweep: read dir %s: %v", c.config.BlackholeDir, err))
		return
	}
	for _, b := range broken {
		logger.Warn(fmt.Sprintf("blackhole sweep: skipping broken pair %s: %s", b.hash, b.reason))
	}

	for _, p := range pairs {
		if ctx.Err() != nil {
			return
		}
		addErr := c.AddFromBuffer(ctx, bytes.NewBuffer(p.torrent), p.opts)
		switch {
		case addErr == nil:
			logger.Info(fmt.Sprintf("blackhole sweep: re-added %s (%s)", p.opts.Name, p.hash))
			removeBlackholePair(c.config.BlackholeDir, p.hash)
		case errors.Is(addErr, model.ErrAlreadyAdded):
			logger.Info(fmt.Sprintf("blackhole sweep: %s (%s) already added", p.opts.Name, p.hash))
			removeBlackholePair(c.config.BlackholeDir, p.hash)
		default:
			logger.Warn(fmt.Sprintf("blackhole sweep: re-add %s (%s) failed: %v", p.opts.Name, p.hash, addErr))
		}
	}
}
