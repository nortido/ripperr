package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/helper/breakeralerts"
	"ripperr/internal/manager"
	"ripperr/internal/runner"
	"ripperr/internal/system/logger"
)

func (s *Server) scheduleLoc(cfg config.App) *time.Location {
	if cfg.Runner.Timezone == "" {
		return nil
	}
	loc, err := time.LoadLocation(cfg.Runner.Timezone)
	if err != nil {
		return nil
	}
	return loc
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	now := time.Now()
	loc := s.scheduleLoc(cfg)
	slots := runner.NextRunsWithin(now, cfg.Runner.StartHour, cfg.Runner.ListRefreshPerDay, 24*time.Hour, loc)
	strSlots := make([]string, len(slots))
	for i, t := range slots {
		strSlots[i] = t.Format(time.RFC3339)
	}
	// хеши нужны только для подсветки видимых строк, полный проход это тысячи
	// записей. Режем по пределу, а длину прохода отдаем отдельным числом
	queued, queuedTotal := s.runnerState.QueuedItems(queuedHashLimit)
	writeJSON(w, http.StatusOK, statusResp{
		Now:         now.Format(time.RFC3339),
		NextRuns:    strSlots,
		Running:     s.runnerState != nil && s.runnerState.Running(),
		DryRun:      s.runnerState != nil && s.runnerState.DryRunning(),
		Stopping:    s.runnerState != nil && s.runnerState.Stopping(),
		Checking:    emptyIfNil(s.runnerState.CheckingItems()),
		Queued:      emptyIfNil(queued),
		QueuedTotal: queuedTotal,
		Alerts:      statusAlerts(s.alerts),
		Redownload:  redownloadSupported(cfg),
	})
}

func statusAlerts(reg *breakeralerts.Registry) []alertResp {
	if reg == nil {
		return []alertResp{}
	}
	incidents := reg.List()
	out := make([]alertResp, len(incidents))
	for i, inc := range incidents {
		out[i] = alertResp{
			Tracker: inc.Tracker,
			Since:   inc.Since.Format(time.RFC3339),
			Error:   inc.LastError,
		}
	}
	return out
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	entries := logger.Snapshot(limit)
	items := make([]logEntryResp, len(entries))
	for i, e := range entries {
		items[i] = logEntryResp{
			Time:    e.Time.Format(time.RFC3339),
			Level:   e.Level,
			Message: e.Message,
		}
	}
	writeJSON(w, http.StatusOK, logsResp{Items: items})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Restart bool `json:"restart"`
		Stop    bool `json:"stop"`
		Dry     bool `json:"dry"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if s.runnerState != nil && s.runnerState.Stopping() {
		writeError(w, http.StatusConflict, "check is stopping")
		return
	}

	if req.Stop {
		if s.runnerState == nil || !s.runnerState.Running() {
			writeError(w, http.StatusConflict, "check not running")
			return
		}
		s.runnerState.DrainTrigger()
		s.runnerState.MarkStopping()
		s.reload()
		writeJSON(w, http.StatusAccepted, runResp{Stopped: true})
		return
	}

	if s.runnerState.WaitingSettings() {
		writeError(w, http.StatusServiceUnavailable, "stored settings are unreadable, check not started")
		return
	}

	if req.Restart && s.runnerState != nil && s.runnerState.Running() {
		s.runnerState.MarkRestart()
		s.reload()
		writeJSON(w, http.StatusAccepted, runResp{Started: true, Restarted: true})
		return
	}

	if s.runnerState != nil && s.runnerState.Running() {
		writeError(w, http.StatusConflict, "check already running")
		return
	}

	if req.Dry {
		if s.runnerState != nil && !s.runnerState.RequestDryRun() {
			writeError(w, http.StatusConflict, "check already queued")
			return
		}
		writeJSON(w, http.StatusAccepted, runResp{Started: true, DryRun: true})
		return
	}

	if s.runnerState != nil && !s.runnerState.RequestRun() {
		writeError(w, http.StatusConflict, "check already queued")
		return
	}
	writeJSON(w, http.StatusAccepted, runResp{Started: true})
}

const queuedHashLimit = 500

func emptyIfNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func redownloadSupported(cfg config.App) bool {
	// условие зеркалит отказы в handleRedownload: клиентам без перекачки кнопку не показываем
	switch manager.NormalizeClient(cfg.Manager.Client) {
	case manager.ClientTransmission, manager.ClientDeluge:
		return false
	}
	return true
}
