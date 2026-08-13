package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"trackerr/internal/config"
	"trackerr/internal/settings"
	trackerPkg "trackerr/internal/tracker"
)

func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	writeJSON(w, http.StatusOK, json.RawMessage(settings.ScheduleJSON(&cfg)))
}

func (s *Server) handlePutSchedule(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err = settings.MergeSchedule(r.Context(), s.db, body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetTrackers(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	items, err := settings.TrackersEffective(r.Context(), &cfg, s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]trackerResp, 0, len(config.TrackerKeys))
	for _, key := range config.TrackerKeys {
		it, ok := items[key]
		if !ok {
			continue
		}
		out = append(out, trackerResp{ID: trackerIDByKey[key], Key: key, TrackerItem: it})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutTracker(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if _, ok := trackerIDByKey[key]; !ok {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err = settings.MergeTrackerKey(r.Context(), s.db, key, body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestTracker(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if _, ok := trackerIDByKey[key]; !ok {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	var req struct {
		Config json.RawMessage `json:"config"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	cfg := s.effective()
	timeout := 30 * time.Second
	if effectiveTrackerProxyType(cfg, key, req.Config) == 1 {
		timeout = time.Duration(cfg.Flaresolverr.MaxTimeout) * time.Millisecond
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	err := trackerPkg.TestLogin(ctx, cfg, s.db, key, req.Config)
	if errors.Is(err, trackerPkg.ErrUnknownTracker) {
		writeError(w, http.StatusNotFound, "unknown tracker key")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, testResultResp{OK: false, Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, testResultResp{OK: true})
}
