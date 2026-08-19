package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"trackerr/internal/model"
	"trackerr/internal/model/storage"
	storagePkg "trackerr/internal/storage"
	"trackerr/internal/system/logger"
)

func queryInt(r *http.Request, key string, def, min, max int) int {
	v, _ := strconv.Atoi(r.URL.Query().Get(key))
	if v < min {
		v = def
	}
	// max <= 0 means no upper clamp; values above max are clamped, not defaulted
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
	// back off from the cut point to the last full rune boundary, so a
	// multi-byte char straddling maxBytes never gets split in half
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(v[cut]) {
		cut--
	}
	return v[:cut]
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 50, 1, 1000)
	offset := queryInt(r, "offset", 0, 0, 0)
	status := r.URL.Query().Get("status")
	ignoredOnly := r.URL.Query().Get("ignored") == "1"
	sortBy := r.URL.Query().Get("sort")
	if sortBy != "recheck" {
		sortBy = "checked"
	}
	nameFilter := queryTrimmed(r, "name", 200)
	trackerID := queryInt(r, "tracker", 0, 1, 0)
	categoryFilter := queryTrimmed(r, "category", 200)
	brokenStreak := s.effective().Runner.ErrorStreakThreshold
	// ignored releases are never rechecked, so the plan view has nothing to
	// show for them; the history view still lists them unless asked to filter
	excludeIgnored := sortBy == "recheck" && !ignoredOnly
	items, total, err := s.db.ListCheckLog(r.Context(), storage.CheckLogFilter{
		Limit:          limit,
		Offset:         offset,
		Status:         status,
		IgnoredOnly:    ignoredOnly,
		ExcludeIgnored: excludeIgnored,
		SortBy:         sortBy,
		Name:           nameFilter,
		BrokenStreak:   brokenStreak,
		TrackerID:      trackerID,
		Category:       categoryFilter,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]historyItemResp, len(items))
	for i, e := range items {
		recheckAt := ""
		if e.RecheckAt != nil {
			recheckAt = e.RecheckAt.Format(time.RFC3339)
		}
		out[i] = historyItemResp{
			ID:          e.ID,
			CheckedAt:   e.CheckedAt.Format(time.RFC3339),
			URLHash:     e.URLHash,
			Name:        e.ItemName,
			URL:         e.ItemURL,
			TrackerID:   e.TrackerTypeID,
			TrackerName: trackerName(e.TrackerTypeID),
			Status:      e.Status,
			Error:       e.Error,
			ErrorStreak: e.ErrorStreak,
			// the threshold lives in the schedule settings, so the flag is
			// resolved here rather than re-derived by every client
			Broken:     brokenStreak >= 1 && e.ErrorStreak >= brokenStreak,
			RecheckAt:  recheckAt,
			ForceCheck: e.ForceCheck,
		}
	}
	writeJSON(w, http.StatusOK, historyResp{Items: out, Total: total})
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
		writeError(w, http.StatusNotFound, "history item not found")
		return
	}
	// the flag alone would only survive until the next scheduled run; asking the
	// runner starts the check now, and several forced releases join one pass.
	// the queue is bounded, so a burst of clicks can still drop the request:
	// the flag stays set and the item is checked on the next scheduled pass
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
	n, err := s.db.ForceCheckAll(r.Context(), force)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"updated": n})
}

func (s *Server) handleRedownload(w http.ResponseWriter, r *http.Request) {
	urlHash := r.PathValue("hash")
	if urlHash == "" {
		writeError(w, http.StatusBadRequest, "hash required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	select {
	case redownloadSlot <- struct{}{}:
		defer func() { <-redownloadSlot }()
	case <-ctx.Done():
		writeError(w, http.StatusGatewayTimeout, "timed out waiting in the redownload queue")
		return
	}

	cfg := s.effective()
	mc := s.qbitManagerConfig(cfg)
	mc.BlackholeDir = cfg.BlackholeDirPath()
	qbc, err := qbitClientFactory(mc)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	entries, err := qbc.List(ctx, model.FilterOptions{})
	if err != nil && !errors.Is(err, model.ErrPartialList) {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	var entry *model.Entry
	for _, e := range entries {
		if storagePkg.URLHash(e.URL) == urlHash {
			entry = e
			break
		}
	}
	if entry == nil {
		writeError(w, http.StatusNotFound, "torrent not found in qbittorrent")
		return
	}

	if err = qbc.ForceRedownload(ctx, entry.Hash); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
	// otherwise the streak the entry was auto-ignored with survives the unignore
	// and the very next error sends it straight back to the ignore list
	if err := s.db.ResetErrorStreak(r.Context(), hash); err != nil {
		logger.Error(fmt.Sprintf("reset error streak for %s: %v", hash, err))
	}
	w.WriteHeader(http.StatusNoContent)
}

// redownloadSlot serializes force redownloads: qBittorrent deletes torrents
// asynchronously, so a re-add running while another deletion is still in flight
// gets silently rejected.
var redownloadSlot = make(chan struct{}, 1)
