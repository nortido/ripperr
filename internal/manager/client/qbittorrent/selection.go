package qbittorrent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ripperr/internal/helper/fileselect"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

const selectionCleanupTimeout = 10 * time.Second

func (c *Client) Files(ctx context.Context, hash string) ([]model.TorrentFile, error) {
	files, err := c.client.GetFilesInformationCtx(ctx, hash)
	if err != nil {
		return nil, err
	}
	out := make([]model.TorrentFile, len(*files))
	for i, f := range *files {
		out[i] = model.TorrentFile{Path: f.Name, Wanted: f.Priority != 0}
	}
	return out, nil
}

func (c *Client) applySelection(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	err := c.setSelection(ctx, hash, opts)
	if err == nil {
		return nil
	}
	// новый торрент без выбора файлов качал бы все, убираем его даже при отмененном ctx
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), selectionCleanupTimeout)
	defer cancel()
	if derr := c.Discard(dctx, hash); derr != nil {
		logger.Warn(fmt.Sprintf("qbit: discard %s after failed file selection: %v", hash, derr))
	}
	return fmt.Errorf("apply file selection to %s: %w", opts.Name, err)
}

func (c *Client) ApplySelection(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	return c.setSelection(ctx, hash, opts)
}

func (c *Client) setSelection(ctx context.Context, hash string, opts model.ManagerAddFileOptions) error {
	files, err := c.Files(ctx, hash)
	if err != nil {
		return err
	}
	// пустой список дал бы Unwanted nil и полную закачку, выбор пользователя потерялся бы
	if len(files) == 0 {
		return fmt.Errorf("empty file list for %s", hash)
	}
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}
	idx := fileselect.Unwanted(opts.FileSelection, paths)
	if len(idx) > 0 {
		ids := make([]string, len(idx))
		for i, n := range idx {
			ids[i] = strconv.Itoa(n)
		}
		if err = c.client.SetFilePriorityCtx(ctx, hash, strings.Join(ids, "|"), 0); err != nil {
			return err
		}
	}
	if !opts.Stopped {
		if err = c.client.StartCtx(ctx, []string{hash}); err != nil {
			return err
		}
	}
	logger.Info(fmt.Sprintf("qbit: file selection for %s: skipped %d of %d files", opts.Name, len(idx), len(paths)))
	return nil
}
