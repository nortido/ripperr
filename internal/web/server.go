package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"trackerr/internal/config"
	"trackerr/internal/helper/breakeralerts"
	"trackerr/internal/manager/client/qbittorrent"
	"trackerr/internal/model"
	"trackerr/internal/model/storage"
	"trackerr/internal/model/tracker"
	"trackerr/internal/runner"
	"trackerr/internal/settings"
	storagePkg "trackerr/internal/storage"
	"trackerr/internal/system/logger"
	trackerPkg "trackerr/internal/tracker"
)

//go:embed static/*
var staticFS embed.FS

type store interface {
	Ping(ctx context.Context) error
	ListCheckLog(ctx context.Context, limit, offset int, statusFilter string, ignoredOnly bool, sortBy string, nameFilter string) ([]storage.CheckLogEntry, int, error)
	ListIgnore(ctx context.Context) ([]storage.IgnoreEntry, error)
	AddIgnore(ctx context.Context, hash, name, url string) error
	DeleteIgnore(ctx context.Context, hash string) error
	GetIgnoreSet(ctx context.Context) (map[string]struct{}, error)
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
	SetForceCheck(ctx context.Context, urlHash string, force bool) (bool, error)
	ForceCheckAll(ctx context.Context, force bool) (int64, error)
	GetTracker(ctx context.Context, trackerID int) (*tracker.Tracker, error)
}

type Server struct {
	db          store
	reload      func()
	effective   func() config.App
	runnerState *runner.State
	alerts      *breakeralerts.Registry
	srv         *http.Server
}

func New(db store, reload func(), effective func() config.App, listen string, runnerState *runner.State, alerts *breakeralerts.Registry) *Server {
	s := &Server{
		db:          db,
		reload:      reload,
		effective:   effective,
		runnerState: runnerState,
		alerts:      alerts,
	}
	mux := http.NewServeMux()
	staticHandler, err := newCachedStaticHandler(staticFS)
	if err != nil {
		panic("static: " + err.Error())
	}
	mux.Handle("/", staticHandler)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/timezones", s.handleGetTimezones)
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("POST /api/run", withMaxBody(s.handleRun))
	mux.HandleFunc("GET /api/history", s.handleHistory)
	mux.HandleFunc("POST /api/plan/{hash}/force", withMaxBody(s.handlePlanForce))
	mux.HandleFunc("POST /api/plan/force-all", withMaxBody(s.handlePlanForceAll))
	mux.HandleFunc("POST /api/history/{hash}/redownload", withMaxBody(s.handleRedownload))
	mux.HandleFunc("GET /api/categories", s.handleGetCategories)
	mux.HandleFunc("GET /api/schedule", s.handleGetSchedule)
	mux.HandleFunc("PUT /api/schedule", withMaxBody(s.handlePutSchedule))
	mux.HandleFunc("GET /api/ignore", s.handleGetIgnore)
	mux.HandleFunc("POST /api/ignore", withMaxBody(s.handlePostIgnore))
	mux.HandleFunc("DELETE /api/ignore/{hash}", s.handleDeleteIgnore)
	mux.HandleFunc("GET /api/trackers", s.handleGetTrackers)
	mux.HandleFunc("PUT /api/trackers/{key}", withMaxBody(s.handlePutTracker))
	mux.HandleFunc("POST /api/trackers/{key}/test", withMaxBody(s.handleTestTracker))
	mux.HandleFunc("GET /api/integrations", s.handleGetIntegrations)
	mux.HandleFunc("PUT /api/integrations", withMaxBody(s.handlePutIntegrations))
	mux.HandleFunc("POST /api/integrations/test", withMaxBody(s.handleTestIntegration))
	s.srv = &http.Server{Addr: listen, Handler: mux}
	return s
}

func (s *Server) Start() error {
	return s.srv.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

const maxRequestBody = 1 << 20

func withMaxBody(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		h(w, r)
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	err := s.db.Ping(ctx)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) scheduleLoc(cfg config.App) *time.Location {
	if cfg.Runner == nil || cfg.Runner.Timezone == "" {
		return nil
	}
	loc, err := time.LoadLocation(cfg.Runner.Timezone)
	if err != nil {
		return nil
	}
	return loc
}

func (s *Server) handleGetTimezones(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"timezones": timezoneEntries()})
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
	writeJSON(w, http.StatusOK, map[string]any{
		"now":       now.Format(time.RFC3339),
		"next_runs": strSlots,
		"running":   s.runnerState != nil && s.runnerState.Running(),
		"alerts":    statusAlerts(s.alerts),
	})
}

func statusAlerts(reg *breakeralerts.Registry) []map[string]string {
	if reg == nil {
		return []map[string]string{}
	}
	incidents := reg.List()
	out := make([]map[string]string, len(incidents))
	for i, inc := range incidents {
		out[i] = map[string]string{
			"tracker": inc.Tracker,
			"since":   inc.Since.Format(time.RFC3339),
			"error":   inc.LastError,
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
	items := make([]map[string]string, len(entries))
	for i, e := range entries {
		items[i] = map[string]string{
			"time":    e.Time.Format(time.RFC3339),
			"level":   e.Level,
			"message": e.Message,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
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
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Restart bool `json:"restart"`
		Stop    bool `json:"stop"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	if req.Stop {
		if s.runnerState == nil || !s.runnerState.Running() {
			writeError(w, http.StatusConflict, "check not running")
			return
		}
		s.runnerState.DrainTrigger()
		s.reload()
		writeJSON(w, http.StatusAccepted, map[string]any{"stopped": true})
		return
	}

	if req.Restart && s.runnerState != nil && s.runnerState.Running() {
		s.runnerState.MarkRestart()
		s.reload()
		writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "restarted": true})
		return
	}

	if s.runnerState != nil && s.runnerState.Running() {
		writeError(w, http.StatusConflict, "check already running")
		return
	}
	if s.runnerState != nil && !s.runnerState.RequestRun() {
		writeError(w, http.StatusConflict, "check already queued")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	status := r.URL.Query().Get("status")
	ignoredOnly := r.URL.Query().Get("ignored") == "1"
	sortBy := r.URL.Query().Get("sort")
	if sortBy != "recheck" {
		sortBy = "checked"
	}
	nameFilter := strings.TrimSpace(r.URL.Query().Get("name"))
	if len(nameFilter) > 200 {
		nameFilter = nameFilter[:200]
	}
	items, total, err := s.db.ListCheckLog(r.Context(), limit, offset, status, ignoredOnly, sortBy, nameFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, len(items))
	for i, e := range items {
		recheckAt := ""
		if e.RecheckAt != nil {
			recheckAt = e.RecheckAt.Format(time.RFC3339)
		}
		out[i] = map[string]any{
			"id":           e.ID,
			"checked_at":   e.CheckedAt.Format(time.RFC3339),
			"url_hash":     e.URLHash,
			"name":         e.ItemName,
			"url":          e.ItemURL,
			"tracker_id":   e.TrackerTypeID,
			"tracker_name": trackerName(e.TrackerTypeID),
			"status":       e.Status,
			"error":        e.Error,
			"recheck_at":   recheckAt,
			"force_check":  e.ForceCheck,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": total})
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
	writeJSON(w, http.StatusOK, map[string]bool{"force": req.Force})
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

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	cfg := s.effective()
	qbc, err := qbitClientFactory(model.ManagerConfig{
		Host:     cfg.Manager.QbitTorrent.Host,
		Username: cfg.Manager.QbitTorrent.Username,
		Password: cfg.Manager.QbitTorrent.Password,
	})
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

func (s *Server) handleGetIgnore(w http.ResponseWriter, r *http.Request) {
	items, err := s.db.ListIgnore(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, len(items))
	for i, e := range items {
		out[i] = map[string]any{
			"url_hash": e.URLHash,
			"name":     e.ItemName,
			"url":      e.ItemURL,
			"added_at": e.AddedAt.Format(time.RFC3339),
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
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetTrackers(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	type trackerResp struct {
		ID           int      `json:"id"`
		Key          string   `json:"key"`
		Name         string   `json:"name"`
		Domain       string   `json:"domain"`
		ActiveDomain string   `json:"active_domain"`
		Mirrors      []string `json:"mirrors"`
		Enabled      bool     `json:"enabled"`
		HourLimit    int      `json:"hour_limit"`
		ProxyType    int      `json:"proxy_type"`
		LoginURL     string   `json:"login_url"`
		DownloadURL  string   `json:"download_url"`
		Credentials  string   `json:"credentials"`
		Cookies      string   `json:"cookies"`
		Publicity    string   `json:"publicity"`
	}
	raw, err := settings.TrackersJSON(r.Context(), &cfg, s.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var m map[string]struct {
		Enabled      bool     `json:"enabled"`
		Domain       string   `json:"domain"`
		ActiveDomain string   `json:"active_domain"`
		Mirrors      []string `json:"mirrors"`
		HourLimit    int      `json:"hour_limit"`
		ProxyType    int      `json:"proxy_type"`
		LoginURL     string   `json:"login_url"`
		DownloadURL  string   `json:"download_url"`
		Credentials  string   `json:"credentials"`
		Cookies      string   `json:"cookies"`
		Publicity    string   `json:"publicity"`
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	names := map[string]string{
		"rutracker": cfg.Tracker.Rutracker.Name,
		"kinozal":   cfg.Tracker.Kinozal.Name,
		"rutor":     cfg.Tracker.Rutor.Name,
		"nnm":       cfg.Tracker.NNM.Name,
		"teamhd":    cfg.Tracker.TeamHD.Name,
	}
	var out []trackerResp
	for _, key := range []string{"rutracker", "kinozal", "rutor", "teamhd", "nnm"} {
		id := trackerIDByKey[key]
		it, ok := m[key]
		if !ok {
			continue
		}
		mirrors := it.Mirrors
		if mirrors == nil {
			mirrors = []string{}
		}
		out = append(out, trackerResp{
			ID: id, Key: key, Name: names[key],
			Domain: it.Domain, ActiveDomain: it.ActiveDomain, Mirrors: mirrors, Enabled: it.Enabled, HourLimit: it.HourLimit,
			ProxyType: it.ProxyType, LoginURL: it.LoginURL, DownloadURL: it.DownloadURL,
			Credentials: it.Credentials, Cookies: it.Cookies, Publicity: it.Publicity,
		})
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

func (s *Server) handleGetIntegrations(w http.ResponseWriter, r *http.Request) {
	cfg := s.effective()
	writeJSON(w, http.StatusOK, json.RawMessage(settings.IntegrationsJSON(&cfg)))
}

func (s *Server) handlePutIntegrations(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err = settings.MergeIntegrations(r.Context(), s.db, body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestIntegration(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string          `json:"kind"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfg := s.effective()
	if len(req.Config) > 0 && string(req.Config) != "null" {
		var err error
		cfg, err = mergeTestIntegrationsConfig(cfg, req.Kind, req.Config)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	switch req.Kind {
	case "qbit":
		_, err := qbittorrent.New(model.ManagerConfig{
			Host:     cfg.Manager.QbitTorrent.Host,
			Username: cfg.Manager.QbitTorrent.Username,
			Password: cfg.Manager.QbitTorrent.Password,
		})
		s.testResult(w, err)
	case "flaresolverr":
		s.testResult(w, testFlaresolverr(cfg))
	case "proxy":
		s.testResult(w, testProxy(cfg))
	default:
		writeError(w, http.StatusBadRequest, "kind must be qbit, flaresolverr, or proxy")
	}
}

func mergeTestIntegrationsConfig(cfg config.App, kind string, raw json.RawMessage) (config.App, error) {
	switch kind {
	case "qbit":
		var c struct {
			Host     string `json:"host"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return cfg, err
		}
		if c.Host != "" {
			norm, err := settings.EnsureScheme(c.Host, "http")
			if err != nil {
				return cfg, err
			}
			cfg.Manager.QbitTorrent.Host = norm
		}
		cfg.Manager.QbitTorrent.Username = c.Username
		cfg.Manager.QbitTorrent.Password = c.Password
	case "flaresolverr":
		var c struct {
			URL       string `json:"url"`
			TimeoutMS int    `json:"timeout_ms"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return cfg, err
		}
		if c.URL != "" {
			norm, err := settings.EnsureScheme(c.URL, "http")
			if err != nil {
				return cfg, err
			}
			cfg.Flaresolverr.Host = norm
		}
		if c.TimeoutMS > 0 {
			cfg.Flaresolverr.MaxTimeout = c.TimeoutMS
		}
	case "proxy":
		var c struct {
			DownloadProxy string `json:"download_proxy"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return cfg, err
		}
		if err := settings.ValidateDownloadProxy(c.DownloadProxy); err != nil {
			return cfg, err
		}
		cfg.Runner.DownloadProxy = c.DownloadProxy
	default:
		return cfg, fmt.Errorf("kind must be qbit, flaresolverr, or proxy")
	}
	return cfg, nil
}

func (s *Server) testResult(w http.ResponseWriter, err error) {
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// qbitCategoriesClient interfaces with qBittorrent; tests may replace qbitClientFactory.
type qbitCategoriesClient interface {
	GetCategories(ctx context.Context) ([]string, error)
	List(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error)
	ForceRedownload(ctx context.Context, torrentHash string) error
}

// ponytail: per-request factory — no long-lived shared qbit client in HTTP handlers
var qbitClientFactory = func(cfg model.ManagerConfig) (qbitCategoriesClient, error) {
	return qbittorrent.New(cfg)
}

func (s *Server) handleGetCategories(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cfg := s.effective()
	qbc, err := qbitClientFactory(model.ManagerConfig{
		Host:     cfg.Manager.QbitTorrent.Host,
		Username: cfg.Manager.QbitTorrent.Username,
		Password: cfg.Manager.QbitTorrent.Password,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	cats, err := qbc.GetCategories(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if cats == nil {
		cats = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": cats})
}

func testFlaresolverr(cfg config.App) error {
	base := cfg.Flaresolverr.Host
	if base == "" {
		return fmt.Errorf("flaresolverr url is empty")
	}
	payload := `{"cmd":"request.get","url":"https://example.com","maxTimeout":` + strconv.Itoa(cfg.Flaresolverr.MaxTimeout) + `}`
	req, err := http.NewRequest(http.MethodPost, base, strings.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("flaresolverr returned %d", resp.StatusCode)
	}
	return nil
}

func testProxy(cfg config.App) error {
	proxyURL := cfg.Runner.DownloadProxy
	if proxyURL == "" {
		return fmt.Errorf("proxy url is empty")
	}
	if err := settings.ValidateDownloadProxy(proxyURL); err != nil {
		return err
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(u)},
	}
	resp, err := client.Get("https://example.com")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}
