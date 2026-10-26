package qbittorrent

import (
	"context"
	"fmt"
	"slices"

	qbt "github.com/autobrr/go-qbittorrent"

	"ripperr/internal/helper/fileselect"
	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

func (c *Client) ConvergeAdded(ctx context.Context, hash string, data []byte, opts model.ManagerAddFileOptions) error {
	if len(opts.FileSelection) == 0 && len(opts.FilePaths) == 0 {
		return nil
	}
	if len(opts.FileSelection) > 0 {
		state, err := c.torrentState(ctx, hash)
		if err != nil {
			return err
		}
		if isChecking(state) || state == qbt.TorrentStateMoving {
			return fmt.Errorf("converge %s: torrent %s is %s, not settled yet", opts.Name, hash, state)
		}
		// запущенный торрент мог запустить пользователь, выбор на ходу не навязываем
		if !isStopped(state) {
			logger.Info(fmt.Sprintf("qbit: converge %s: torrent %s is %s, left as is", opts.Name, hash, state))
			return nil
		}
	}
	files, err := c.Files(ctx, hash)
	if err != nil {
		return err
	}
	// снятый файл значит, что выбор уже ставили мы или пользователь: не трогаем ни выбор, ни имена.
	// Если он ровно наш, прошлый запуск упал на старте: только запускаем
	if len(opts.FileSelection) > 0 && slices.ContainsFunc(files, func(f model.TorrentFile) bool { return !f.Wanted }) {
		if opts.Stopped || !fileselect.Applied(opts.FileSelection, files, selectionPaths(files, opts)) {
			return nil
		}
		logger.Info(fmt.Sprintf("qbit: converge %s: file selection already set, starting %s", opts.Name, hash))
		if err = c.client.StartCtx(ctx, []string{hash}); err != nil {
			return fmt.Errorf("converge %s: start %s: %w", opts.Name, hash, err)
		}
		return nil
	}
	if len(opts.FilePaths) > 0 {
		if renamedSinceAdd(data, files) {
			logger.Info(fmt.Sprintf("qbit: converge %s: names changed after the add, renames skipped", opts.Name))
		} else {
			c.restorePaths(ctx, hash, opts)
		}
	}
	if len(opts.FileSelection) == 0 {
		return nil
	}
	logger.Info(fmt.Sprintf("qbit: converge %s: applying file selection", opts.Name))
	// в отличие от applySelection без discard: тот же хеш мог добавить сам пользователь, это живая загрузка.
	// Торрент остается, вызывающий держит пару и повторит
	if err = c.setSelection(ctx, hash, opts); err != nil {
		return fmt.Errorf("converge %s: apply file selection to %s: %w", opts.Name, hash, err)
	}
	return nil
}

func renamedSinceAdd(data []byte, files []model.TorrentFile) bool {
	orig, err := torrentmeta.FilePaths(data)
	if err != nil || len(orig) != len(files) {
		return false
	}
	for i, f := range files {
		if f.Path != orig[i] {
			return true
		}
	}
	return false
}
