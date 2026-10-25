package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ripperr/internal/helper"
	"ripperr/internal/model/storage"
	"ripperr/internal/system/logger"
)

const (
	uiEventTextMax     = 1000
	uiEventPathMax     = 300
	uiEventsPerMinute  = 20
	defaultEventsLimit = 200
	maxEventsLimit     = 500
)

type eventResp struct {
	ID      int64  `json:"id"`
	Time    string `json:"time"`
	Source  string `json:"source"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Subject string `json:"subject"`
	AppPath string `json:"app_path"`
}

type eventsResp struct {
	Items []eventResp `json:"items"`
}

func (s *Server) handleGetEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = defaultEventsLimit
	}
	if limit > maxEventsLimit {
		limit = maxEventsLimit
	}
	items := []eventResp{}
	if s.events != nil {
		entries, err := s.events.ListEvents(r.Context(), limit)
		if err != nil {
			logger.Error(fmt.Sprintf("events: list: %v", err))
			writeError(w, http.StatusInternalServerError, "event storage error")
			return
		}
		for _, e := range entries {
			items = append(items, eventResp{
				ID:      e.ID,
				Time:    e.At.Format(time.RFC3339),
				Source:  e.Source,
				Kind:    e.Kind,
				Text:    e.Text,
				Subject: e.Subject,
				AppPath: e.AppPath,
			})
		}
	}
	writeJSON(w, http.StatusOK, eventsResp{Items: items})
}

func (s *Server) handlePostEvent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeError(w, http.StatusBadRequest, "text required")
		return
	}
	if s.events == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !s.allowUIEvent(time.Now()) {
		writeError(w, http.StatusTooManyRequests, "too many events")
		return
	}
	e := storage.EventEntry{
		At:      time.Now(),
		Source:  "ui",
		Kind:    "ui_error",
		Text:    helper.Substr(text, 0, uiEventTextMax),
		AppPath: sanitizeEventPath(req.Path),
	}
	if err := s.events.AddEvent(r.Context(), e); err != nil {
		logger.Error(fmt.Sprintf("events: add: %v", err))
		writeError(w, http.StatusInternalServerError, "event storage error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) allowUIEvent(now time.Time) bool {
	s.uiEventMu.Lock()
	defer s.uiEventMu.Unlock()
	if s.uiEventStart.IsZero() || now.Sub(s.uiEventStart) >= time.Minute {
		s.uiEventStart = now
		s.uiEventCount = 0
	}
	if s.uiEventCount >= uiEventsPerMinute {
		return false
	}
	s.uiEventCount++
	return true
}

func sanitizeEventPath(p string) string {
	if !strings.HasPrefix(p, "#") || len([]rune(p)) > uiEventPathMax {
		return ""
	}
	for _, r := range p {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return ""
		}
	}
	return p
}
