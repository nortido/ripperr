package qbittorrent

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"ripperr/internal/helper/fileselect"
	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

var (
	renamePoll           = 200 * time.Millisecond
	renameVisibleTimeout = 5 * time.Second
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
	// qbit отвечает 409, пока целевое имя занято текущим, и меняет имена асинхронно.
	// Поэтому переименования идут раундами по модели текущих имен, цикл рвем временным именем
	c.renameFiles(ctx, hash, opts.Name, cur, want)
}

func (c *Client) renameFiles(ctx context.Context, hash, name string, cur, want []string) {
	names := slices.Clone(cur)
	pending := map[int]bool{}
	for i := range names {
		if names[i] != want[i] {
			pending[i] = true
		}
	}
	var first error
	failed := 0
	fail := func(i int, err error) {
		delete(pending, i)
		failed++
		if first == nil {
			first = err
		}
	}
	for len(pending) > 0 {
		ready, blocked := renameRound(names, want, pending, fail)
		moves := map[int]string{}
		for _, i := range ready {
			moves[i] = want[i]
		}
		if len(ready) == 0 && len(blocked) > 0 {
			moves[blocked[0]] = tempName(names[blocked[0]], names, want)
		}
		for _, i := range slices.Sorted(maps.Keys(moves)) {
			err := c.client.RenameFileCtx(ctx, hash, names[i], moves[i])
			if err != nil {
				fail(i, err)
				delete(moves, i)
				if ctx.Err() != nil {
					pending = nil
					break
				}
				continue
			}
			names[i] = moves[i]
			if names[i] == want[i] {
				delete(pending, i)
			}
		}
		if len(pending) == 0 {
			break
		}
		if len(moves) == 0 {
			continue
		}
		if err := c.waitRenamesVisible(ctx, hash, names, moves); err != nil {
			logger.Warn(fmt.Sprintf("qbit: restore names for %s: %d renames left: %v", name, len(pending), err))
			break
		}
	}
	if failed > 0 {
		logger.Warn(fmt.Sprintf("qbit: restore names for %s: %d renames failed, first: %v", name, failed, first))
	}
	for i := range names {
		if names[i] != cur[i] && names[i] != want[i] {
			logger.Warn(fmt.Sprintf("qbit: restore names for %s: file left as %s", name, names[i]))
		}
	}
}

func renameRound(names, want []string, pending map[int]bool, fail func(int, error)) ([]int, []int) {
	holder := make(map[string]int, len(names))
	for i, n := range names {
		holder[n] = i
	}
	for dropped := true; dropped; {
		dropped = false
		for _, i := range slices.Sorted(maps.Keys(pending)) {
			if h, ok := holder[want[i]]; ok && !pending[h] {
				fail(i, fmt.Errorf("%s: %s is in use", names[i], want[i]))
				dropped = true
			}
		}
	}
	var ready, blocked []int
	for _, i := range slices.Sorted(maps.Keys(pending)) {
		if _, ok := holder[want[i]]; ok {
			blocked = append(blocked, i)
		} else {
			ready = append(ready, i)
		}
	}
	return ready, blocked
}

func tempName(base string, names, want []string) string {
	tmp := base + ".ripperr-tmp"
	for n := 2; slices.Contains(names, tmp) || slices.Contains(want, tmp); n++ {
		tmp = fmt.Sprintf("%s.ripperr-tmp%d", base, n)
	}
	return tmp
}

func (c *Client) waitRenamesVisible(ctx context.Context, hash string, names []string, moves map[int]string) error {
	deadline := time.Now().Add(renameVisibleTimeout)
	for {
		files, err := c.Files(ctx, hash)
		if err == nil && len(files) == len(names) && renamesShown(files, moves) {
			return nil
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = fmt.Errorf("not visible after %s", renameVisibleTimeout)
			}
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(renamePoll):
		}
	}
}

func renamesShown(files []model.TorrentFile, moves map[int]string) bool {
	for i, to := range moves {
		if files[i].Path != to {
			return false
		}
	}
	return true
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
