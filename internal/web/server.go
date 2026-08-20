package web

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"time"

	"trackerr/internal/config"
	"trackerr/internal/helper/breakeralerts"
	"trackerr/internal/model/storage"
	"trackerr/internal/model/tracker"
	"trackerr/internal/runner"
)

//go:embed static/*
var staticFS embed.FS

type store interface {
	Ping(ctx context.Context) error
	ListCheckLog(ctx context.Context, f storage.CheckLogFilter) ([]storage.CheckLogEntry, int, error)
	CheckLogSummary(ctx context.Context) (storage.CheckLogSummary, error)
	ListIgnore(ctx context.Context) ([]storage.IgnoreEntry, error)
	AddIgnore(ctx context.Context, hash, name, url string) error
	DeleteIgnore(ctx context.Context, hash string) error
	ResetErrorStreak(ctx context.Context, urlHash string) error
	GetIgnoreSet(ctx context.Context) (map[string]struct{}, error)
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
	SetForceCheck(ctx context.Context, urlHash string, force bool) (bool, error)
	ForceCheckAll(ctx context.Context, force bool, f storage.CheckLogFilter) (hashes []string, n int64, err error)
	GetTracker(ctx context.Context, trackerID int) (*tracker.Tracker, error)
}

type Server struct {
	db          store
	reload      func()
	effective   func() config.App
	runnerState *runner.State
	alerts      *breakeralerts.Registry
	srv         *http.Server
	authCache   authCache
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
	mux.HandleFunc("GET /api/auth/state", s.handleAuthState)
	mux.HandleFunc("POST /api/auth/setup", withMaxBody(s.handleAuthSetup))
	mux.HandleFunc("POST /api/auth/login", withMaxBody(s.handleAuthLogin))
	mux.HandleFunc("POST /api/auth/logout", withMaxBody(s.handleAuthLogout))
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("POST /api/run", withMaxBody(s.handleRun))
	mux.HandleFunc("GET /api/history", s.handleHistory)
	mux.HandleFunc("GET /api/history/summary", s.handleHistorySummary)
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
	s.srv = &http.Server{Addr: listen, Handler: s.withAuth(mux)}
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
	writeJSON(w, status, errorResp{Error: msg})
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
		writeJSON(w, http.StatusServiceUnavailable, healthResp{Status: "error", Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, healthResp{Status: "ok"})
}
