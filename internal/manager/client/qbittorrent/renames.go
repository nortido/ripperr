package qbittorrent

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"ripperr/internal/helper/fileselect"
	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

func renamedPaths(data []byte, files []model.TorrentFile) []string {
	cur := make([]string, len(files))
	for i, f := range files {
		cur[i] = f.Path
	}
	// при ошибке разбора или другом числе файлов имена все равно везем: ложная тревога
	// стоит только лишнего чтения списка файлов, а пропуск потерял бы переименования
	if orig, err := torrentmeta.FilePaths(data); err == nil && slices.Equal(orig, cur) {
		return nil
	}
	return cur
}

func (c *Client) restorePaths(ctx context.Context, hash string, opts model.ManagerAddFileOptions) {
	files, err := c.Files(ctx, hash)
	if err != nil {
		logger.Warn(fmt.Sprintf("qbit: restore names for %s: read files: %v", opts.Name, err))
		return
	}
	want := opts.FilePaths
	if len(files) != len(want) {
		logger.Warn(fmt.Sprintf("qbit: restore names for %s: %d files, saved %d names, skipped", opts.Name, len(files), len(want)))
		return
	}
	cur := make([]string, len(files))
	for i, f := range files {
		cur[i] = f.Path
	}
	if slices.Equal(cur, want) {
		return
	}
	if oldRoot, newRoot, ok := rootRename(cur, want); ok {
		if err = c.client.RenameFolderCtx(ctx, hash, oldRoot, newRoot); err != nil {
			logger.Warn(fmt.Sprintf("qbit: restore names for %s: rename folder: %v", opts.Name, err))
		}
		return
	}
	// у того же infohash позиции файлов совпадают. Каждый вызов берет нетронутый исходный путь файла,
	// поэтому асинхронные переименования в qbit друг другу не мешают
	var first error
	failed := 0
	for i := range cur {
		if cur[i] == want[i] {
			continue
		}
		if err = c.client.RenameFileCtx(ctx, hash, cur[i], want[i]); err != nil {
			failed++
			if first == nil {
				first = err
			}
			if ctx.Err() != nil {
				break
			}
		}
	}
	if failed > 0 {
		logger.Warn(fmt.Sprintf("qbit: restore names for %s: %d renames failed, first: %v", opts.Name, failed, first))
	}
}

func rootRename(cur, want []string) (string, string, bool) {
	oldRoot, okOld := fileselect.CommonRoot(cur)
	newRoot, okNew := fileselect.CommonRoot(want)
	if !okOld || !okNew || oldRoot == newRoot {
		return "", "", false
	}
	for i := range cur {
		if strings.TrimPrefix(cur[i], oldRoot+"/") != strings.TrimPrefix(want[i], newRoot+"/") {
			return "", "", false
		}
	}
	return oldRoot, newRoot, true
}
