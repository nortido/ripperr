package web

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"ripperr/internal/model"
	"ripperr/internal/settings"
)

type onboardingResp struct {
	ChecklistHidden bool `json:"checklist_hidden"`
	TourDone        bool `json:"tour_done"`
	Replay          bool `json:"replay"`
	ClientOK        bool `json:"client_ok"`
	TrackerOK       bool `json:"tracker_ok"`
	WatchOK         bool `json:"watch_ok"`
}

func (s *Server) handleGetOnboarding(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")
	doc, err := settings.GetOnboarding(ctx, s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := onboardingResp{ChecklistHidden: doc.ChecklistHidden, TourDone: doc.TourDone, Replay: doc.Replay}
	if doc.ChecklistHidden {
		writeJSON(w, http.StatusOK, out)
		return
	}
	added, err := s.addedTrackerKeys(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out.TrackerOK = len(added) > 0
	cfg := s.effective()
	if out.WatchOK, err = settings.WatchChosen(ctx, s.db, &cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out.ClientOK = s.clientReachable(ctx)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handlePutOnboarding(w http.ResponseWriter, r *http.Request) {
	var p settings.OnboardingPatch
	if err := decodeStrict(r.Body, &p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if p.ChecklistHidden == nil && p.TourDone == nil && p.Replay == nil {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}
	if err := settings.MergeOnboarding(r.Context(), s.db, p); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var clientProbeTimeout = 5 * time.Second

const (
	clientProbeTTL     = 60 * time.Second
	clientProbeBgLimit = 30 * time.Second
)

type clientFlight struct {
	done chan struct{}
	key  string
	ok   bool
}

type clientProbeCache struct {
	mu     sync.Mutex
	key    string
	ok     bool
	at     time.Time
	flight *clientFlight
}

func (s *Server) clientReachable(ctx context.Context) bool {
	// Без лога при ошибке: на свежей установке клиента еще нет, и каждый заход на главную писал бы строку.
	// Результат кешируем: каждая попытка входа с неверным паролем приближает бан IP в qBittorrent
	cfg := s.effective()
	mcfg := s.managerConfig(cfg)
	key := strings.Join([]string{cfg.Manager.Client, mcfg.Host, mcfg.Username, mcfg.Password}, "\x00")
	c := &s.clientProbe
	c.mu.Lock()
	if c.key == key && !c.at.IsZero() && time.Since(c.at) < clientProbeTTL {
		ok := c.ok
		c.mu.Unlock()
		return ok
	}
	fl := c.flight
	if fl == nil || fl.key != key {
		fl = &clientFlight{done: make(chan struct{}), key: key}
		c.flight = fl
		go s.runClientProbe(fl, cfg.Manager.Client, mcfg)
	}
	c.mu.Unlock()
	timer := time.NewTimer(clientProbeTimeout)
	defer timer.Stop()
	select {
	case <-fl.done:
		return fl.ok
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func (s *Server) runClientProbe(fl *clientFlight, kind string, mcfg model.ManagerConfig) {
	// Вход в клиент идет без ctx и может зависнуть, поэтому проба живет отдельно от запроса и дописывает кеш позже
	ctx, cancel := context.WithTimeout(context.Background(), clientProbeBgLimit)
	defer cancel()
	if mc, err := managerClientFactory(kind, mcfg); err == nil {
		_, err = mc.GetCategories(ctx)
		fl.ok = err == nil
	}
	c := &s.clientProbe
	c.mu.Lock()
	if c.flight == fl {
		c.flight = nil
		c.key, c.ok, c.at = fl.key, fl.ok, time.Now()
	}
	c.mu.Unlock()
	close(fl.done)
}
