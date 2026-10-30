package runner

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
	trackerModel "ripperr/internal/model/tracker"
	storagePkg "ripperr/internal/storage"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker/client"
)

func runBound(run, req context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(req)
	stop := context.AfterFunc(run, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (h *handler) releaseTorrents(ctx context.Context, urlHash string) (targets, others []*model.Entry, adoptErr error, err error) {
	all, err := h.manager.GetEntries(ctx, model.FilterOptions{})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list torrents: %w", err)
	}
	for _, e := range all {
		url := e.URL
		// строку истории торрента без комментария проход пишет по адресу усыновления
		if url == "" {
			var aerr error
			if url, aerr = h.trackerService.Adopt(ctx, e.Name, e.Hash); aerr != nil {
				url = ""
				if adoptErr == nil {
					adoptErr = fmt.Errorf("adopt %s: %w", e.Name, aerr)
				}
			}
		}
		if url != "" && storagePkg.URLHash(url) == urlHash {
			targets = append(targets, e)
		} else {
			others = append(others, e)
		}
	}
	return targets, others, adoptErr, nil
}

func contentRoot(e *model.Entry) []string {
	root := e.ContentPath
	if root == "" {
		root = e.SavePath + "/" + e.Name
	}
	// клиент на Windows отдает пути с обратной чертой, а filepath на Linux ее не делит
	root = path.Clean(strings.ReplaceAll(root, `\`, "/"))
	return strings.FieldsFunc(root, func(r rune) bool { return r == '/' })
}

func rootsOverlap(a, b []string) bool {
	n := min(len(a), len(b))
	for i := range n {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func filesDeletable(targets, others []*model.Entry) bool {
	if len(targets) == 0 {
		return false
	}
	// настройки удаления правят только замену, ручное удаление сдерживает лишь общая папка
	for _, t := range targets {
		if t.SavePath == "" && t.ContentPath == "" {
			return false
		}
	}
	// вложенные корни в любую сторону делят файлы: удаление папки снесет данные соседа
	for _, t := range targets {
		tr := contentRoot(t)
		for _, o := range others {
			// место соседа неизвестно: его файлы могут лежать внутри
			if (o.SavePath == "" && o.ContentPath == "") || rootsOverlap(tr, contentRoot(o)) {
				return false
			}
		}
	}
	return true
}

func deleteOptions(e *model.Entry, files bool) model.ManagerDeleteOptions {
	return model.ManagerDeleteOptions{Hash: e.Hash, Category: e.Category, Name: e.Name, DeleteFiles: files}
}

func (h *handler) removalInfo(ctx context.Context, urlHash string) (RemovalInfo, error) {
	targets, others, _, err := h.releaseTorrents(ctx, urlHash)
	if err != nil {
		return RemovalInfo{}, err
	}
	return RemovalInfo{Torrents: len(targets), Files: filesDeletable(targets, others)}, nil
}

func (h *handler) removeRelease(ctx context.Context, req RemoveRequest) error {
	targets, _, adoptErr, err := h.releaseTorrents(ctx, req.URLHash)
	if err != nil {
		return err
	}
	if len(targets) != req.Torrents {
		return ErrTorrentsChanged
	}
	// неусыновленный торрент может быть этой раздачей: без него строка ушла бы при живой загрузке
	if len(targets) == 0 && adoptErr != nil {
		return fmt.Errorf("remove %s: %w: %v", req.URLHash, model.ErrPartialList, adoptErr)
	}
	name, files := req.URLHash, false
	if len(targets) > 0 {
		locked := make(map[string]struct{}, len(targets))
		for _, t := range targets {
			key := strings.ToLower(t.Hash)
			if _, ok := locked[key]; ok {
				continue
			}
			unlock, ok := h.state.TryLockTorrent(t.Hash)
			if !ok {
				return ErrReleaseBusy
			}
			defer unlock()
			locked[key] = struct{}{}
		}
		// замена, закончившаяся во время первого списка, оставила бы новый торрент без строки
		cur, others, _, err := h.releaseTorrents(ctx, req.URLHash)
		if err != nil {
			return err
		}
		seen := make(map[string]struct{}, len(cur))
		for _, t := range cur {
			key := strings.ToLower(t.Hash)
			if _, ok := locked[key]; !ok {
				return ErrTorrentsChanged
			}
			seen[key] = struct{}{}
		}
		if len(cur) != len(targets) || len(seen) != len(locked) {
			return ErrTorrentsChanged
		}
		if req.Files && !filesDeletable(cur, others) {
			return fmt.Errorf("remove %s: %w", req.URLHash, model.ErrFilesNotAllowed)
		}
		opts := make([]model.ManagerDeleteOptions, 0, len(cur))
		for _, t := range cur {
			opts = append(opts, deleteOptions(t, req.Files))
		}
		accepted, err := h.manager.Remove(ctx, opts)
		h.state.MarkRemoved(accepted...)
		if err != nil {
			return err
		}
		name, files = cur[0].Name, req.Files
	}
	// торренты уже удалены: отмена запроса не должна оставить строку без торрента
	dctx := context.WithoutCancel(ctx)
	if err := h.storage.DeleteItem(dctx, req.URLHash); err != nil {
		return fmt.Errorf("delete history %s: %w", req.URLHash, err)
	}
	if err := h.storage.DeleteIgnore(dctx, req.URLHash); err != nil {
		return fmt.Errorf("delete ignore %s: %w", req.URLHash, err)
	}
	logger.Info(fmt.Sprintf("release removed by user: %s, torrents %d, files %t", name, len(targets), files))
	return nil
}

func (h *handler) addWatched(req AddRequest) bool {
	// форма отдает одно из двух по режиму слежки: чужая пара значит подмену запроса
	if len(h.config.WatchTags) > 0 {
		if req.Category != "" {
			return false
		}
	} else if req.Tag != "" {
		return false
	}
	return h.watched(&model.Entry{Category: req.Category, Tag: req.Tag})
}

func (h *handler) fetchTopicTorrent(ctx context.Context, ttid int, tc client.IClient, link string) ([]byte, error) {
	page, err := h.getPage(ctx, ttid, link)
	if err != nil {
		return nil, fmt.Errorf("topic page: %w", err)
	}
	encoded := bodyEncoded(tc)
	// Miss тут не ошибка: торрент еще ничем не занят, берем то, что нашлось
	id := tc.PickTorrent(page, encoded, trackerModel.Held{URL: link}).DownloadID
	if id == "" {
		id = tc.ParseDownloadID(page, encoded)
	}
	magnet := tc.ParseMagnet(page, encoded)
	if id == "" && magnet == "" {
		return nil, ErrNoTorrent
	}
	var dlErr error
	if id != "" {
		var b *bytes.Buffer
		if b, dlErr = h.trackerService.Response(ctx, ttid, tc.DownloadLink(id), true); dlErr == nil {
			if _, dlErr = torrentmeta.InfoHash(b.Bytes()); dlErr == nil {
				return b.Bytes(), nil
			}
		}
		if magnet == "" {
			return nil, fmt.Errorf("download torrent: %w", dlErr)
		}
	}
	data, magErr := h.buildTorrentFromMagnet(ctx, magnet, link)
	if magErr == nil {
		return data, nil
	}
	if dlErr != nil {
		return nil, fmt.Errorf("download torrent: %v; magnet metadata: %w", dlErr, magErr)
	}
	return nil, fmt.Errorf("magnet metadata: %w", magErr)
}

func (h *handler) addRelease(ctx context.Context, req AddRequest) (AddResult, error) {
	link := strings.TrimSpace(req.URL)
	ttid := h.trackerService.GetID(link)
	if ttid == 0 {
		return AddResult{}, ErrUnknownTracker
	}
	if !h.addWatched(req) {
		return AddResult{}, ErrNotWatched
	}
	tc, err := h.trackerService.GetCachedClient(ctx, ttid)
	if err != nil {
		return AddResult{}, fmt.Errorf("tracker client: %w", err)
	}
	primary := primaryDomain(tc)
	if primary == "" {
		return AddResult{}, ErrUnknownTracker
	}
	// запросы идут только на хост трекера, а не на тот, что ввел пользователь
	link = trackerModel.ReplaceURLHost(link, primary)
	data, err := h.fetchTopicTorrent(ctx, ttid, tc, link)
	if err != nil {
		return AddResult{}, err
	}
	comment, err := torrentmeta.Comment(data)
	if err != nil {
		return AddResult{}, fmt.Errorf("read torrent comment: %w", err)
	}
	// своя ссылка трекера остается, как в submit; чужой или пустой комментарий заменяем
	if comment == "" || h.trackerService.GetID(comment) == 0 {
		if data, err = torrentmeta.SetComment(data, link); err != nil {
			return AddResult{}, fmt.Errorf("set torrent comment: %w", err)
		}
		comment = link
	}
	hash, err := torrentmeta.InfoHash(data)
	if err != nil {
		return AddResult{}, fmt.Errorf("torrent hash: %w", err)
	}
	unlock, ok := h.state.TryLockTorrent(hash)
	if !ok {
		return AddResult{}, ErrReleaseBusy
	}
	defer unlock()
	opts := model.ManagerAddFileOptions{Category: req.Category, Tag: req.Tag, AutoManaged: req.Category != "", Comment: comment}
	if err = h.manager.Add(ctx, data, opts); err != nil {
		return AddResult{}, fmt.Errorf("add torrent: %w", err)
	}
	// торрент уже в клиенте: сбой дальше не должен выглядеть как неудачное добавление
	dctx := context.WithoutCancel(ctx)
	key := storagePkg.URLHash(comment)
	var res AddResult
	if ignored, ierr := h.storage.GetIgnoreSet(dctx); ierr != nil {
		logger.Warn(fmt.Sprintf("added by link, ignore set not read: %v", ierr))
	} else if _, ok = ignored[key]; ok {
		if ierr = h.storage.DeleteIgnore(dctx, key); ierr != nil {
			logger.Warn(fmt.Sprintf("added by link, ignore %s not removed: %v", key, ierr))
		} else {
			res.Unignored = true
			if ierr = h.storage.ResetErrorStreak(dctx, key); ierr != nil {
				logger.Error(fmt.Sprintf("reset error streak for %s: %v", key, ierr))
			}
		}
	}
	h.state.ClearRemoved(time.Now(), []string{hash})
	// кэш PollNew мог бы пропустить регистрацию, поэтому зовем опрос напрямую
	if _, err = h.pollNew(ctx); err != nil {
		logger.Warn(fmt.Sprintf("added by link, poll failed: %v", err))
	}
	logger.Info(fmt.Sprintf("added by link: %s, category %q, tag %q", comment, req.Category, req.Tag))
	return res, nil
}
