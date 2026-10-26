package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"ripperr/internal/config"
	"ripperr/internal/manager"
	"ripperr/internal/manager/client/deluge"
	"ripperr/internal/manager/client/transmission"
	"ripperr/internal/model"
	"ripperr/internal/model/storage"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/runner"
	storagePkg "ripperr/internal/storage"
	"ripperr/internal/system/logger"
	trackerPkg "ripperr/internal/tracker"
	"ripperr/internal/tracker/registry"
)

const pollNewTimeout = 20 * time.Second

func queryInt(r *http.Request, key string, def, min, max int) int {
	v, _ := strconv.Atoi(r.URL.Query().Get(key))
	if v < min {
		v = def
	}
	// max <= 0: без верхней границы. Что выше max, обрезаем до max, а не сбрасываем
	if max > 0 && v > max {
		v = max
	}
	return v
}

func queryTrimmed(r *http.Request, key string, maxBytes int) string {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if len(v) <= maxBytes {
		return v
	}
	// откатываемся к началу руны, чтобы не разрезать символ на границе maxBytes
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(v[cut]) {
		cut--
	}
	return v[:cut]
}

func (s *Server) checkLogFilterFromQuery(r *http.Request) storage.CheckLogFilter {
	// один разбор на список и на форс всем, иначе форс набирал бы не те записи, что видны
	sortBy := r.URL.Query().Get("sort")
	if sortBy != "recheck" {
		sortBy = "checked"
	}
	ignoredOnly := r.URL.Query().Get("ignored") == "1"
	return storage.CheckLogFilter{
		Status:      r.URL.Query().Get("status"),
		IgnoredOnly: ignoredOnly,
		// игнорируемые не перепроверяются, в плане им делать нечего. В истории
		// они видны, пока их не отфильтруют
		ExcludeIgnored: sortBy == "recheck" && !ignoredOnly,
		SortBy:         sortBy,
		Name:           queryTrimmed(r, "name", 200),
		BrokenStreak:   s.effective().Runner.ErrorStreakThreshold,
		TrackerID:      queryInt(r, "tracker", 0, 1, 0),
		Category:       queryTrimmed(r, "category", 200),
	}
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	f := s.checkLogFilterFromQuery(r)
	f.Limit = queryInt(r, "limit", 50, 1, 1000)
	f.Offset = queryInt(r, "offset", 0, 0, 0)
	items, total, err := s.db.ListCheckLog(r.Context(), f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// метки ставит опрос, здесь только читаем один снимок на весь ответ
	absent := s.runnerState.Absent()
	out := make([]historyItemResp, len(items))
	for i, e := range items {
		recheckAt := ""
		if e.RecheckAt != nil {
			recheckAt = e.RecheckAt.Format(time.RFC3339)
		}
		// у новой записи проверки еще не было
		checkedAt := ""
		if !e.CheckedAt.IsZero() {
			checkedAt = e.CheckedAt.Format(time.RFC3339)
		}
		_, isAbsent := absent[e.URLHash]
		out[i] = historyItemResp{
			ID:          e.ID,
			CheckedAt:   checkedAt,
			URLHash:     e.URLHash,
			Name:        e.ItemName,
			URL:         e.ItemURL,
			TrackerID:   e.TrackerTypeID,
			TrackerName: trackerName(e.TrackerTypeID),
			Status:      e.Status,
			Error:       e.Error,
			ErrorStreak: e.ErrorStreak,
			Category:    e.Category,
			// порог лежит в настройках расписания, так что флаг считаем тут,
			// а не в каждом клиенте
			Broken:     f.BrokenStreak >= 1 && e.ErrorStreak >= f.BrokenStreak,
			RecheckAt:  recheckAt,
			ForceCheck: e.ForceCheck,
			Absent:     isAbsent,
		}
	}
	writeJSON(w, http.StatusOK, historyResp{Items: out, Total: total})
}

func (s *Server) handlePollNew(w http.ResponseWriter, r *http.Request) {
	if s.runnerState.WaitingSettings() {
		writeError(w, http.StatusServiceUnavailable, "stored settings are unreadable, poll not started")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), pollNewTimeout)
	defer cancel()
	n, err := s.runnerState.PollNew(ctx)
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, runner.ErrPollUnavailable) {
			code = http.StatusServiceUnavailable
		}
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pollNewResp{Changed: n})
}

func (s *Server) handleHistorySummary(w http.ResponseWriter, r *http.Request) {
	sum, err := s.db.CheckLogSummary(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, checkLogSummaryResp{
		Updated: sum.Updated,
		Errors:  sum.Errors,
	})
}

func (s *Server) handlePlanForce(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if hash == "" {
		writeError(w, http.StatusBadRequest, "hash required")
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ok, err := s.db.SetForceCheck(r.Context(), hash, req.Force)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		if req.Force {
			newRel, err := s.db.IsNewRelease(r.Context(), hash)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if newRel {
				// истории у новой записи нет, флагу негде жить: просим проверку сразу
				writeJSON(w, http.StatusOK, map[string]bool{"force": false, "force_queued": s.runnerState.RequestForce(hash)})
				return
			}
		}
		writeError(w, http.StatusNotFound, "history item not found")
		return
	}
	// один флаг дождался бы только планового запуска, а просьба к раннеру проверяет сразу,
	// и несколько форсов идут одним проходом. Очередь ограничена, шквал кликов может
	// потерять запрос, но флаг останется и запись проверится в плановом проходе
	queued := true
	if req.Force {
		queued = s.runnerState.RequestForce(hash)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"force": req.Force, "force_queued": queued})
}

func (s *Server) handlePlanForceAll(w http.ResponseWriter, r *http.Request) {
	force := true
	if r.Body != nil {
		var req struct {
			Force *bool `json:"force"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Force != nil {
			force = *req.Force
		}
	}
	hashes, n, err := s.db.ForceCheckAll(r.Context(), force, s.checkLogFilterFromQuery(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// как и с одиночным форсом, одного флага мало, кнопка должна проверять сразу.
	// Не влезшие в очередь релизы сохраняют флаг и уйдут в плановый прогон
	queued := 0
	if force {
		for _, hash := range hashes {
			if s.runnerState.RequestForce(hash) {
				queued++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]int64{"updated": n, "queued": int64(queued)})
}

func (s *Server) handleRedownload(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.writableConfig()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, settingsUnreadableMsg)
		return
	}
	if manager.NormalizeClient(cfg.Manager.Client) == manager.ClientTransmission {
		// это ясно и без клиента: не занимаем семафор перекачек и не тянем список
		// торрентов ради ошибки, которая от них не зависит. errors.Is ниже
		// страхует прямой вызов ForceRedownload
		writeError(w, http.StatusNotImplemented, transmission.ErrRedownloadUnsupported.Error())
		return
	}
	if manager.NormalizeClient(cfg.Manager.Client) == manager.ClientDeluge {
		writeError(w, http.StatusNotImplemented, deluge.ErrRedownloadUnsupported.Error())
		return
	}

	urlHash := r.PathValue("hash")
	if urlHash == "" {
		writeError(w, http.StatusBadRequest, "hash required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	// перекачки строго по одной: qBittorrent удаляет торренты асинхронно, и
	// повторное добавление до конца удаления молча отвергается
	select {
	case redownloadSlot <- struct{}{}:
		defer func() { <-redownloadSlot }()
	case <-ctx.Done():
		writeError(w, http.StatusGatewayTimeout, "timed out waiting in the redownload queue")
		return
	}

	mcfg := s.managerConfig(cfg)
	mcfg.BlackholeDir = cfg.BlackholeDirPath()
	mc, err := managerClientFactory(cfg.Manager.Client, mcfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	entries, err := mc.List(ctx, model.FilterOptions{})
	if err != nil && !errors.Is(err, model.ErrPartialList) {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	partialList := errors.Is(err, model.ErrPartialList)

	var entry *model.Entry
	for _, e := range entries {
		if storagePkg.URLHash(e.URL) == urlHash {
			entry = e
			break
		}
	}
	if entry == nil {
		writeError(w, http.StatusNotFound, "torrent not found in the download client")
		return
	}

	newComment := primaryTopicURL(cfg, entry.URL)
	newHash := storagePkg.URLHash(newComment)
	var ignored *storage.IgnoreEntry
	if newComment != "" && newHash != urlHash {
		// игнор привязан к хешу комментария: читаем его до перекачки, чтобы
		// при сбое БД не потерять отметку на уже перекачанном торренте
		items, lerr := s.db.ListIgnore(ctx)
		if lerr != nil {
			writeError(w, http.StatusInternalServerError, lerr.Error())
			return
		}
		for i := range items {
			if items[i].URLHash == urlHash {
				ignored = &items[i]
				break
			}
		}
	}

	unlockTorrent, free := s.runnerState.TryLockTorrent(entry.Hash)
	if !free {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "torrent is being updated by a check, try again later",
			"code":  "torrent_busy",
		})
		return
	}
	defer unlockTorrent()

	if err = mc.ForceRedownload(ctx, entry.Hash, model.RedownloadOptions{Comment: newComment}); err != nil {
		if errors.Is(err, transmission.ErrRedownloadUnsupported) {
			writeError(w, http.StatusNotImplemented, err.Error())
			return
		}
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	if ignored != nil {
		// торрент уже перекачан: отметку переносим даже если клиент отключился
		ctx := context.WithoutCancel(ctx)
		// сначала новая запись, потом удаление старой: при сбое второй записи отметка остаеться
		if err = s.db.AddIgnore(ctx, newHash, ignored.ItemName, newComment); err != nil {
			writeError(w, http.StatusInternalServerError, "redownloaded, but the ignore mark was not moved: "+err.Error())
			return
		}
		sibling := partialList
		for _, e := range entries {
			if e != entry && storagePkg.URLHash(e.URL) == urlHash {
				sibling = true
				break
			}
		}
		// старая отметка еще прикрывает торрент-близнец с тем же комментарием.
		// В неполном списке близнец мог не попасть, поэтому там ее не трогаем
		if !sibling {
			if err = s.db.DeleteIgnore(ctx, urlHash); err != nil {
				logger.Error(fmt.Sprintf("delete old ignore mark %s: %v", urlHash, err))
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func primaryTopicURL(cfg config.App, comment string) string {
	if u, err := url.Parse(comment); err != nil || u.Host == "" {
		return ""
	}
	// трекер ищем как раннер: Detect включенных трекеров из действующего конфига
	trCfg := trackerPkg.ConfigFromApp(cfg)
	for _, d := range registry.All() {
		t, ok := trCfg[d.Key]
		if !ok || !t.Enabled || !d.New(t).Detect(comment) {
			continue
		}
		primary := strings.TrimSpace(t.Domain)
		if out := trackerModel.ReplaceURLHost(comment, primary); out != comment {
			return out
		}
		return ""
	}
	return ""
}

func (s *Server) handleGetIgnore(w http.ResponseWriter, r *http.Request) {
	items, err := s.db.ListIgnore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]ignoreItemResp, len(items))
	for i, e := range items {
		out[i] = ignoreItemResp{
			URLHash: e.URLHash,
			Name:    e.ItemName,
			URL:     e.ItemURL,
			AddedAt: e.AddedAt.Format(time.RFC3339),
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePostIgnore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URLHash string `json:"url_hash"`
		URL     string `json:"url"`
		Name    string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash := req.URLHash
	if hash == "" {
		if req.URL == "" {
			writeError(w, http.StatusBadRequest, "url or url_hash required")
			return
		}
		hash = storagePkg.URLHash(req.URL)
	}
	if err := s.db.AddIgnore(r.Context(), hash, req.Name, req.URL); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (s *Server) handleDeleteIgnore(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	if hash == "" {
		writeError(w, http.StatusBadRequest, "hash required")
		return
	}
	if err := s.db.DeleteIgnore(r.Context(), hash); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// иначе серия ошибок переживет снятие игнора, и первая же ошибка вернет
	// запись обратно в игнор
	if err := s.db.ResetErrorStreak(r.Context(), hash); err != nil {
		logger.Error(fmt.Sprintf("reset error streak for %s: %v", hash, err))
	}
	w.WriteHeader(http.StatusNoContent)
}

var redownloadSlot = make(chan struct{}, 1)
