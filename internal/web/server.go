package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/helper/breakeralerts"
	"ripperr/internal/model/storage"
	"ripperr/internal/model/tracker"
	"ripperr/internal/runner"
	"ripperr/internal/system/logger"
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
	IsNewRelease(ctx context.Context, urlHash string) (bool, error)
	ForceCheckAll(ctx context.Context, force bool, f storage.CheckLogFilter) (hashes []string, n int64, err error)
	GetTracker(ctx context.Context, trackerID int) (*tracker.Tracker, error)
	PurgeCategoriesExcept(ctx context.Context, allowed []string) (int64, error)
	DeleteTracker(ctx context.Context, trackerID int) error
}

type eventStore interface {
	AddEvent(ctx context.Context, e storage.EventEntry) error
	ListEvents(ctx context.Context, limit int) ([]storage.EventEntry, error)
}

type Server struct {
	db           store
	events       eventStore
	reload       func()
	effective    func() config.App
	strict       func() (config.App, error)
	runnerState  *runner.State
	alerts       *breakeralerts.Registry
	srv          *http.Server
	authCache    authCache
	authMu       sync.Mutex
	loginFails   *loginThrottle
	deviceFails  *loginThrottle
	hashSlots    chan struct{}
	listen       string
	lnMu         sync.Mutex
	ln           net.Listener
	lnHost       string
	closed       bool
	done         chan struct{}
	serveErr     chan error
	uiEventMu    sync.Mutex
	uiEventStart time.Time
	uiEventCount int
	clientProbe  clientProbeCache
}

func New(db store, reload func(), effective func() config.App, listen string, runnerState *runner.State, alerts *breakeralerts.Registry) *Server {
	s := &Server{
		db:          db,
		reload:      reload,
		effective:   effective,
		runnerState: runnerState,
		alerts:      alerts,
		loginFails:  newLoginThrottle(),
		deviceFails: newLoginThrottle(),
		hashSlots:   make(chan struct{}, pbkdf2Slots),
		listen:      listen,
		done:        make(chan struct{}),
		serveErr:    make(chan error, 1),
	}
	mux := http.NewServeMux()
	staticHandler, err := newCachedStaticHandler(staticFS)
	if err != nil {
		panic("static: " + err.Error())
	}
	mux.Handle("/", staticHandler)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
	mux.HandleFunc("GET /api/auth/state", s.handleAuthState)
	mux.HandleFunc("POST /api/auth/setup", withMaxBody(s.handleAuthSetup))
	mux.HandleFunc("POST /api/auth/login", withMaxBody(s.handleAuthLogin))
	mux.HandleFunc("POST /api/auth/logout", withMaxBody(s.handleAuthLogout))
	mux.HandleFunc("POST /api/auth/password", withMaxBody(s.handleAuthPassword))
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/events", s.handleGetEvents)
	mux.HandleFunc("POST /api/events", withMaxBody(s.handlePostEvent))
	mux.HandleFunc("POST /api/run", withMaxBody(s.handleRun))
	mux.HandleFunc("GET /api/history", s.handleHistory)
	mux.HandleFunc("GET /api/history/summary", s.handleHistorySummary)
	mux.HandleFunc("POST /api/history/poll", withMaxBody(s.handlePollNew))
	mux.HandleFunc("POST /api/plan/{hash}/force", withMaxBody(s.handlePlanForce))
	mux.HandleFunc("POST /api/plan/force-all", withMaxBody(s.handlePlanForceAll))
	mux.HandleFunc("GET /api/history/add", s.handleAddOptions)
	mux.HandleFunc("POST /api/history/add", withMaxBody(s.handleAddRelease))
	mux.HandleFunc("GET /api/history/{hash}/remove", s.handleRemovalInfo)
	mux.HandleFunc("POST /api/history/{hash}/remove", withMaxBody(s.handleRemoveRelease))
	mux.HandleFunc("POST /api/history/{hash}/redownload", withMaxBody(s.handleRedownload))
	mux.HandleFunc("GET /api/categories", s.handleGetCategories)
	mux.HandleFunc("GET /api/schedule", s.handleGetSchedule)
	mux.HandleFunc("PUT /api/schedule", withMaxBody(s.handlePutSchedule))
	mux.HandleFunc("GET /api/ignore", s.handleGetIgnore)
	mux.HandleFunc("POST /api/ignore", withMaxBody(s.handlePostIgnore))
	mux.HandleFunc("DELETE /api/ignore/{hash}", s.handleDeleteIgnore)
	mux.HandleFunc("GET /api/trackers", s.handleGetTrackers)
	mux.HandleFunc("GET /api/trackers/types", s.handleGetTrackerTypes)
	mux.HandleFunc("PUT /api/trackers/order", withMaxBody(s.handlePutTrackersOrder))
	mux.HandleFunc("PUT /api/trackers/{key}", withMaxBody(s.handlePutTracker))
	mux.HandleFunc("POST /api/trackers/{key}", withMaxBody(s.handleAddTracker))
	mux.HandleFunc("DELETE /api/trackers/{key}", s.handleRemoveTracker)
	mux.HandleFunc("POST /api/trackers/{key}/test", withMaxBody(s.handleTestTracker))
	mux.HandleFunc("POST /api/trackers/{key}/secret", withMaxBody(s.handleRevealTrackerSecret))
	mux.HandleFunc("POST /api/trackers/{key}/mirrors/confirm", withMaxBody(s.handleConfirmMirror))
	mux.HandleFunc("POST /api/trackers/{key}/suggestions/dismiss", withMaxBody(s.handleDismissSuggestion))
	mux.HandleFunc("GET /api/integrations", s.handleGetIntegrations)
	mux.HandleFunc("PUT /api/integrations", withMaxBody(s.handlePutIntegrations))
	mux.HandleFunc("POST /api/integrations/test", withMaxBody(s.handleTestIntegration))
	mux.HandleFunc("POST /api/integrations/secret", withMaxBody(s.handleRevealIntegrationSecret))
	mux.HandleFunc("GET /api/web", s.handleGetWeb)
	mux.HandleFunc("PUT /api/web", withMaxBody(s.handlePutWeb))
	mux.HandleFunc("GET /api/onboarding", s.handleGetOnboarding)
	mux.HandleFunc("PUT /api/onboarding", withMaxBody(s.handlePutOnboarding))
	mux.HandleFunc("GET /api/push", s.handleGetPush)
	mux.HandleFunc("POST /api/push/devices", withMaxBody(s.handlePostPushDevice))
	mux.HandleFunc("DELETE /api/push/devices/{id}", s.handleDeletePushDevice)
	// без ReadTimeout: net/http держит его и в обработчике и отменил бы r.Context()
	// долгих запросов (проверка трекера, перекачка), поэтому срок ставим только на тело
	s.srv = &http.Server{
		Addr:              listen,
		Handler:           withBodyDeadline(withSecurityHeaders(s.withAuth(mux))),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s
}

func (s *Server) SetEvents(e eventStore) {
	s.events = e
}

func (s *Server) SetStrictEffective(f func() (config.App, error)) {
	s.strict = f
}

const settingsUnreadableMsg = "stored settings are unreadable, try again later"

func (s *Server) writableConfig() (config.App, error) {
	if s.strict != nil {
		return s.strict()
	}
	return s.effective(), nil
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Start() error {
	s.lnMu.Lock()
	if s.closed {
		s.lnMu.Unlock()
		return http.ErrServerClosed
	}
	addr := s.effective().Web.Listen
	ln, err := net.Listen("tcp", addr)
	if err != nil && addr != s.listen {
		logger.Warn(fmt.Sprintf("web: cannot listen on %s: %v, using %s", addr, err, s.listen))
		addr = s.listen
		ln, err = net.Listen("tcp", addr)
	}
	if err != nil {
		s.lnMu.Unlock()
		return err
	}
	s.lnHost, _, _ = net.SplitHostPort(addr)
	s.ln = ln
	s.serve(ln)
	s.lnMu.Unlock()

	select {
	case <-s.done:
		return http.ErrServerClosed
	case err = <-s.serveErr:
		return err
	}
}

func (s *Server) serve(ln net.Listener) {
	go func() {
		err := s.srv.Serve(ln)
		s.lnMu.Lock()
		defer s.lnMu.Unlock()
		// старый листенер после переезда закрываем сами, его ошибка ожидаема
		if s.ln != ln || errors.Is(err, http.ErrServerClosed) {
			return
		}
		s.ln = nil
		s.serveErr <- err
	}()
}

func (s *Server) Addr() net.Addr {
	s.lnMu.Lock()
	defer s.lnMu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.lnMu.Lock()
	if !s.closed {
		s.closed = true
		s.ln = nil
		close(s.done)
	}
	s.lnMu.Unlock()
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

var bodyReadTimeout = 30 * time.Second

func withBodyDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}
		rc := http.NewResponseController(w)
		if err := rc.SetReadDeadline(time.Now().Add(bodyReadTimeout)); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		r.Body = &deadlineBody{ReadCloser: r.Body, rc: rc}
		next.ServeHTTP(w, r)
	})
}

type deadlineBody struct {
	io.ReadCloser
	rc  *http.ResponseController
	eof bool
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) && !b.eof {
		b.eof = true
		// после EOF сервер сам читает соединение в фоне, срок в нем отменил бы контекст обработчика
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	return n, err
}

func withMaxBody(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		h(w, r)
	}
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResp{Status: "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		// текст ошибки только в лог: проба открыта без авторизации
		logger.Warn(fmt.Sprintf("readyz: db ping failed: %v", err))
		writeJSON(w, http.StatusServiceUnavailable, healthResp{Status: "error"})
		return
	}
	writeJSON(w, http.StatusOK, healthResp{Status: "ok"})
}
