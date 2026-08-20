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
	// blackholeReachableTimeout bounds the reachability probe: without it a
	// hung qbit blocks the sweeper goroutine forever, and app.Run waits on
	// that goroutine before returning, so reload hangs too.
	blackholeReachableTimeout = 30 * time.Second
	// blackholeAddTimeout is a safety net against a hung HTTP call, not a
	// queue regulator: it must stay longer than metadataFetchTimeout, or a
	// magnet with opts.Comment set (addMagnetWithComment) gets its context
	// killed mid-waitForMetadata, the error comes back as
	// context.DeadlineExceeded (not errFetchMetadataUnsupported or
	// errMetadataFetchTimeout), the without-comment fallback never triggers,
	// and even if it did it would run with the same already-expired context.
	// A slow swarm would then never re-add, forever, one wasted minute per
	// tick. Delaying whatever else is queued behind it in the same tick is
	// accepted.
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
		// don't leave an orphan .torrent behind a failed write, it would look
		// like a broken pair to the sweeper forever
		if wroteTorrent {
			_ = os.Remove(torrentPath)
		}
		return fmt.Errorf("write blackhole metadata: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	// for a magnet-only pair the json is the only copy of the safety net, so a
	// truncate-then-write os.WriteFile can lose it to a crash mid-write; write
	// to a sibling temp file, fsync, then rename over the target instead
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
		// fsync isn't supported on every filesystem; don't kill the whole
		// write over it, just note it (the rename below still needs its own
		// directory fsync to be durable, that one's handled separately)
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
	// the rename itself isn't durable until the directory entry is flushed:
	// without this, a crash right after Rename can still lose the file on
	// some filesystems even though Rename returned. Same story as the file
	// fsync above: not fatal, not every filesystem supports it.
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

func (c *Client) ConfirmSettled(ctx context.Context, hash string) bool { return c.blackholeSettled(ctx, hash) }

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
		// files with any other extension are left alone entirely, not even logged
		switch ext := filepath.Ext(name); ext {
		case ".torrent":
			// lowercased so the hash matches what removeBlackholePair rebuilds
			// (blackholePaths always lowercases), or a mixed-case pair on disk
			// would revive every tick forever, unremovable
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
	// meta embeds ManagerAddFileOptions flatly, so a .json written by the old
	// code (just the options, no Magnet key) unmarshals here too, with Magnet
	// left at its zero value: no migration needed for existing pairs.
	opts = meta.ManagerAddFileOptions
	// a json body of "null" or "{}" unmarshals with no error and a zero opts:
	// catch it here instead of restoring a torrent with no category/tags/name
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
		// a bad .torrent next to a working magnet is still recoverable, don't
		// let it rot as broken forever, but keep the read error visible: it's
		// the only signal a failing disk gives before every pair goes broken
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
	// a non-empty .torrent can still be a truncated bencode blob left by a
	// crash mid-write; a magnet fallback means AddFromBuffer never gets
	// handed bytes that torrentmeta.InfoHash will just fail on again
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
	// qbit's delete-with-files job runs async: the torrent leaves torrents/list
	// the instant delete is called, but the file removal keeps going after
	// that. A fresh add reporting success (or ErrAlreadyAdded) only means the
	// torrent is in the list right now, not that no delete job is still
	// racing to take it and its files down again. Wait out the same delay
	// ForceRedownload relies on before trusting presence, and don't use the
	// per-add ctx for it, that one's already canceled by the caller.
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
		// a broken record is only a liability while the torrent it was meant
		// to protect is missing; if the torrent is already in the client the
		// record protects nothing and can go, same call ErrAlreadyAdded
		// already makes for a valid pair below
		// except on a hash mismatch: there the bytes on disk belong to some
		// other infohash than the filename, so the filename's torrent being
		// present says nothing about what this record protects
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
