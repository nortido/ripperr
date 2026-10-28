package manager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ripperr/internal/helper/fileselect"
	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
	"ripperr/internal/system/logger"
)

func spaceNeeded(data []byte, oldFiles, selection []model.TorrentFile, deletingFirst bool) int64 {
	paths, sizes, err := torrentmeta.FileSizes(data)
	if err != nil {
		return 0
	}
	done := make(map[string]int64, len(oldFiles))
	// при удалении старого первым его файлы не в зачет: их снесут до запуска нового,
	// а хардлинки могут держать место на диске
	if !deletingFirst {
		for _, f := range oldFiles {
			done[f.Path] = f.Done
		}
	}
	skip := fileselect.Unwanted(selection, paths)
	// qbit без подпапки отдает пути без корня: зачет по пути внутри корня нового торрента
	root, rooted := fileselect.CommonRoot(paths)
	var need int64
	for i, p := range paths {
		if slices.Contains(skip, i) {
			continue
		}
		d, ok := done[p]
		if !ok && rooted {
			d = done[strings.TrimPrefix(p, root+"/")]
		}
		need += max(0, sizes[i]-d)
	}
	return need
}

func (m *manager) checkSpace(ctx context.Context, data []byte, newHash string, oldFiles []model.TorrentFile, opts model.ManagerAddFileOptions, deletingFirst bool) error {
	need := spaceNeeded(data, oldFiles, opts.FileSelection, deletingFirst)
	if need <= 0 {
		return nil
	}
	if opts.SavePath == "" {
		logger.Info(fmt.Sprint("free space check skipped, no save path: ", opts.Name))
		return nil
	}
	free, err := m.client.FreeSpace(ctx, opts.SavePath)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		msg := fmt.Sprintf("free space check skipped for %s: %v", opts.Name, err)
		if errors.Is(err, model.ErrFreeSpaceUnknown) {
			logger.Info(msg)
		} else {
			logger.Warn(msg)
		}
		return nil
	}
	if free >= need {
		return nil
	}
	// новый уже в клиенте с прошлого прогона и место занял: замену доводят ветки ниже
	present, err := m.client.Exists(ctx, newHash)
	if err != nil {
		return fmt.Errorf("check presence of new torrent for %s: %w", opts.Name, err)
	}
	if present {
		return nil
	}
	return &model.NoSpaceError{Path: opts.SavePath, Need: need, Free: free, Hash: newHash}
}
