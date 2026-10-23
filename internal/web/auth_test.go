package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sethvargo/go-envconfig"
	"ripperr/internal/config"
	"ripperr/internal/runner"
	"ripperr/internal/storage/client/sqlite"
	"ripperr/internal/system/logger"
)

type countingSettingStore struct {
	fakeStore
	getSettingCalls int
}

func (c *countingSettingStore) GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error) {
	c.getSettingCalls++
	return c.fakeStore.GetSetting(ctx, key)
}

func TestAuthSetupThenLoginFlow(t *testing.T) {
	store := &fakeStore{}
	srv := newBareServer(store)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = config.Web{Listen: ":0", Enabled: true, TrustedProxy: false}
		return cfg
	}

	body := []byte(`{"username":"testuser","password":"testpass123"}`)
	req := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(body))
	req.Host = "192.168.1.5:6410"
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("setup failed: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var setupResp map[string]bool
	_ = json.Unmarshal(w.Body.Bytes(), &setupResp)
	if !setupResp["ok"] {
		t.Error("setup response ok=false")
	}

	setupCookie := w.Header().Get("Set-Cookie")
	if setupCookie == "" {
		t.Error("setup did not set cookie")
	}

	req = httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(body))
	req.Host = "192.168.1.5:6410"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate setup: expected 409, got %d", w.Code)
	}

	loginBody := []byte(`{"username":"testuser","password":"testpass123"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("login failed: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	loginCookie := w.Header().Get("Set-Cookie")
	if loginCookie == "" {
		t.Error("login did not set cookie")
	}

	wrongBody := []byte(`{"username":"testuser","password":"wrongpass"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(wrongBody))
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("login with wrong password: expected 401, got %d", w.Code)
	}
}

func TestAuthBlocksApiWhenConfigured(t *testing.T) {
	store := &fakeStore{}
	srv := newBareServer(store)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = config.Web{Listen: ":0", Enabled: true, TrustedProxy: false}
		return cfg
	}

	setupBody := []byte(`{"username":"admin","password":"adminpass123"}`)
	req := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(setupBody))
	req.Host = "192.168.1.5:6410"
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup failed: %d", w.Code)
	}

	req = httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unauth api call: expected 401, got %d", w.Code)
	}

	loginBody := []byte(`{"username":"admin","password":"adminpass123"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d", w.Code)
	}

	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == authCookieName {
			cookie = c
			break
		}
	}
	if cookie == nil {
		t.Fatalf("no session cookie found")
	}

	req = httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("auth api call with cookie should not be 401, got %d", w.Code)
	}
}

func TestLoadAuthCachesAcrossRequests(t *testing.T) {
	store := &countingSettingStore{}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/history", nil)
		req.RemoteAddr = "203.0.113.5:1234"
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: code %d, want 401", i, w.Code)
		}
	}
	if store.getSettingCalls != 1 {
		t.Fatalf("GetSetting calls = %d, want 1 (second request should hit the cache)", store.getSettingCalls)
	}
}

func TestLoadAuthCacheUpdatedOnSetup(t *testing.T) {
	store := &countingSettingStore{}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = config.Web{Listen: ":0", Enabled: true, TrustedProxy: false}
		return cfg
	}

	req := httptest.NewRequest("GET", "/api/auth/state", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if store.getSettingCalls != 1 {
		t.Fatalf("GetSetting calls after warmup = %d, want 1", store.getSettingCalls)
	}

	setupBody := []byte(`{"username":"admin","password":"newpass123"}`)
	req = httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(setupBody))
	req.Host = "192.168.1.5:6410"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup failed: %d, body: %s", w.Code, w.Body.String())
	}
	if store.getSettingCalls != 2 {
		t.Fatalf("GetSetting calls after setup = %d, want 2 (setup rereads the DB once under authMu)", store.getSettingCalls)
	}

	req = httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth request after setup: expected 401, got %d", w.Code)
	}
	if store.getSettingCalls != 2 {
		t.Fatalf("GetSetting calls after protected request = %d, want 2 (cache went stale)", store.getSettingCalls)
	}

	wrongBody := []byte(`{"username":"admin","password":"wrongpass"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(wrongBody))
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("login with wrong password: expected 401, got %d", w.Code)
	}

	loginBody := []byte(`{"username":"admin","password":"newpass123"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login with new password: expected 200, got %d, body: %s", w.Code, w.Body.String())
	}

	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == authCookieName {
			cookie = c
			break
		}
	}
	if cookie == nil {
		t.Fatal("login did not set session cookie")
	}

	req = httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Fatal("authorized request with new session cookie got 401")
	}

	if store.getSettingCalls != 2 {
		t.Fatalf("GetSetting calls at end = %d, want 2 (cache must serve every read)", store.getSettingCalls)
	}
}

func TestLoginPageServed(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = config.Web{Listen: ":0", Enabled: true, TrustedProxy: false}
		return cfg
	}

	req := httptest.NewRequest("GET", "/login", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /login: expected 200, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`id="auth-card"`)) {
		t.Error("login page should contain id=\"auth-card\"")
	}
}

func newConfiguredAuthServer(t *testing.T, web config.Web) *Server {
	t.Helper()
	srv := newBareServer(&fakeStore{})
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = web
		return cfg
	}
	setupBody := []byte(`{"username":"admin","password":"adminpass123"}`)
	req := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(setupBody))
	req.Host = "192.168.1.5:6410"
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup failed: %d", w.Code)
	}
	return srv
}

func TestCrossOriginStateChangeBlocked(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		origin string
		auth   bool
		want   int
	}{
		{"logout foreign origin", "POST", "/api/auth/logout", "http://evil.example", false, http.StatusForbidden},
		{"logout same origin", "POST", "/api/auth/logout", "http://192.168.1.5:6410", false, http.StatusOK},
		{"logout no origin", "POST", "/api/auth/logout", "", false, http.StatusOK},
		{"logout null origin", "POST", "/api/auth/logout", "null", false, http.StatusForbidden},
		{"session delete foreign origin", "DELETE", "/api/ignore/abc", "http://evil.example", true, http.StatusForbidden},
		{"session delete same origin", "DELETE", "/api/ignore/abc", "http://192.168.1.5:6410", true, http.StatusNoContent},
		{"session get foreign origin", "GET", "/api/history", "http://evil.example", true, http.StatusOK},
		{"no auth force-all foreign origin", "POST", "/api/plan/force-all", "http://evil.example", false, http.StatusForbidden},
		{"password foreign origin", "POST", "/api/auth/password", "http://evil.example", true, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newBareServer(&fakeStore{})
			var cookie *http.Cookie
			if tc.auth {
				srv = newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
				cookie = loginCookie(t, srv)
			}
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if cookie != nil {
				req.AddCookie(cookie)
			}
			req.RemoteAddr = "192.168.1.10:1234"
			req.Host = "192.168.1.5:6410"
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			srv.srv.Handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("code %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestCrossOriginAllowedHostOrigin(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		want    int
	}{
		{"allowed host behind proxy", []string{"my.keenetic.pro"}, http.StatusOK},
		{"not in allowed hosts", nil, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(&fakeStore{})
			srv.effective = func() config.App {
				cfg := testEffective()
				cfg.Web = config.Web{Listen: ":0", AllowedHosts: tc.allowed}
				return cfg
			}
			req := httptest.NewRequest("POST", "/api/auth/logout", nil)
			req.RemoteAddr = "192.168.1.10:1234"
			req.Host = "192.168.1.200:6410"
			req.Header.Set("Origin", "https://my.keenetic.pro")
			w := httptest.NewRecorder()
			srv.srv.Handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("code %d, want %d", w.Code, tc.want)
			}
		})
	}
}

type lockedSettingStore struct {
	fakeStore
	mu sync.Mutex
}

func (l *lockedSettingStore) GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeStore.GetSetting(ctx, key)
}

func (l *lockedSettingStore) SaveSetting(ctx context.Context, key string, value json.RawMessage) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.fakeStore.SaveSetting(ctx, key, value)
}

func authState(t *testing.T, srv *Server, cookie *http.Cookie) map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/auth/state", nil)
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = "192.168.1.5:6410"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("auth/state: %d", w.Code)
	}
	var state map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func sessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == authCookieName {
			return c
		}
	}
	return nil
}

func TestLegacyLanBypassEnvIgnored(t *testing.T) {
	cfg := config.DefaultApp()
	if err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.MapLookuper(map[string]string{"WEB_LAN_BYPASS": "true", "WEB_TRUSTED_PROXY": "true"}),
	}); err != nil {
		t.Fatal(err)
	}
	srv := newConfiguredAuthServer(t, cfg.Web)
	cases := []struct{ remote, host, xff string }{
		{"192.168.1.10:1234", "192.168.1.5:6410", ""},
		{"192.168.1.10:1234", "localhost:6410", ""},
		{"192.168.1.10:1234", "[::1]:6410", ""},
		{"192.168.1.10:1234", "nas:6410", ""},
		{"192.168.1.10:1234", "ripperr.lan", ""},
		{"172.18.0.1:5000", "192.168.1.5:6410", "8.8.8.8, 192.168.1.20"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", "/api/history", nil)
		req.RemoteAddr = tc.remote
		req.Host = tc.host
		if tc.xff != "" {
			req.Header.Set("X-Forwarded-For", tc.xff)
		}
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s via %s xff %q: code %d, want 401", tc.host, tc.remote, tc.xff, w.Code)
		}
	}
}

func TestApiClosedUntilAccount(t *testing.T) {
	srv := newBareServer(&fakeStore{})
	cases := []struct{ method, path, body string }{
		{"GET", "/api/history", ""},
		{"GET", "/api/status", ""},
		{"POST", "/api/run", ""},
		{"POST", "/api/integrations/secret", `{"field":"qbit.password"}`},
		{"POST", "/api/auth/password", `{"current":"x","new":"yyyyyyyy"}`},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.RemoteAddr = "192.168.1.10:1234"
		req.Host = "192.168.1.5:6410"
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "account setup required") {
			t.Errorf("%s %s: code %d body %s, want 401 account setup required", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	state := authState(t, srv, nil)
	if state["configured"] != false || state["authenticated"] != false {
		t.Errorf("state = %v, want configured false, authenticated false", state)
	}
}

func TestSetupHostCheck(t *testing.T) {
	keenetic := []string{"my.keenetic.pro"}
	cases := []struct {
		host    string
		allowed []string
		want    int
	}{
		{"192.168.1.5:6410", nil, http.StatusOK},
		{"localhost:6410", nil, http.StatusOK},
		{"[::1]:6410", nil, http.StatusOK},
		{"nas:6410", nil, http.StatusOK},
		{"ripperr.lan", nil, http.StatusOK},
		{"box.home.arpa", nil, http.StatusOK},
		{"svc.internal", nil, http.StatusOK},
		{"nas.local.", nil, http.StatusOK},
		{"MY.Keenetic.PRO", keenetic, http.StatusOK},
		{"evil.example.com:6410", nil, http.StatusForbidden},
		{"evil.example.com:6410", keenetic, http.StatusForbidden},
		{"my.keenetic.pro", nil, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			srv := newBareServer(&fakeStore{})
			srv.effective = func() config.App {
				cfg := testEffective()
				cfg.Web = config.Web{Listen: ":0", Enabled: true, AllowedHosts: tc.allowed}
				return cfg
			}
			req := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(`{"username":"admin","password":"adminpass123"}`))
			req.RemoteAddr = "192.168.1.10:1234"
			req.Host = tc.host
			w := httptest.NewRecorder()
			srv.srv.Handler.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("setup code %d, want %d, body %s", w.Code, tc.want, w.Body.String())
			}
			if tc.want == http.StatusForbidden {
				if state := authState(t, srv, nil); state["configured"] != false {
					t.Errorf("configured = %v after rejected setup, want false", state["configured"])
				}
			}
		})
	}
}

func TestSetupCreatesOneAccount(t *testing.T) {
	srv := New(&lockedSettingStore{}, func() {}, testEffective, ":0", runner.NewState(), nil)
	const n = 8
	recs := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`{"username":"admin","password":"adminpass%d"}`, i)
			req := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(body))
			req.RemoteAddr = "192.168.1.10:1234"
			req.Host = "192.168.1.5:6410"
			recs[i] = httptest.NewRecorder()
			srv.srv.Handler.ServeHTTP(recs[i], req)
		}()
	}
	wg.Wait()

	winner := -1
	oks := 0
	for i, w := range recs {
		switch w.Code {
		case http.StatusOK:
			oks++
			winner = i
		case http.StatusConflict, http.StatusTooManyRequests:
		default:
			t.Errorf("setup %d: code %d, want 200, 409 or 429", i, w.Code)
		}
	}
	if oks != 1 {
		t.Fatalf("successful setups = %d, want exactly 1", oks)
	}

	req := httptest.NewRequest("GET", "/api/history", nil)
	req.AddCookie(sessionCookie(recs[winner]))
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("history with winner cookie: code %d, want 200", w.Code)
	}

	req = httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(fmt.Sprintf(`{"username":"admin","password":"adminpass%d"}`, winner)))
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("login with winner password: code %d, want 200", w.Code)
	}
}

func TestAuthStateHidesUsername(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	state := authState(t, srv, nil)
	if state["username"] != "" {
		t.Errorf("username without cookie = %v, want empty", state["username"])
	}
	if _, ok := state["lan_bypass"]; ok {
		t.Errorf("state has lan_bypass key: %v", state)
	}

	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"adminpass123"}`))
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d", w.Code)
	}
	state = authState(t, srv, sessionCookie(w))
	if state["username"] != "admin" || state["authenticated"] != true {
		t.Errorf("state with cookie = %v, want username admin, authenticated true", state)
	}
}

func loginCookie(t *testing.T, srv *Server) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"adminpass123"}`))
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = "192.168.1.5:6410"
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	cookie := sessionCookie(w)
	if w.Code != http.StatusOK || cookie == nil {
		t.Fatalf("login: code %d, cookie %v", w.Code, cookie)
	}
	return cookie
}

func TestSetupRechecksStoredAccount(t *testing.T) {
	stored := json.RawMessage(`{"username":"owner","salt":"c2FsdA","hash":"aGFzaA","secret":"c2VjcmV0","created_at":"2026-10-22T00:00:00Z"}`)
	store := &fakeStore{settings: map[string]json.RawMessage{sqlite.SettingAuth: stored}}
	srv := newBareServer(store)
	// так кеш выглядит, если чтение до setup записало nil уже после сохранения аккаунта
	srv.authCache.set(nil)

	req := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(`{"username":"admin","password":"adminpass123"}`))
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = "192.168.1.5:6410"
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("setup over stored account: code %d, want 409", w.Code)
	}
	if got := string(store.settings[sqlite.SettingAuth]); got != string(stored) {
		t.Errorf("stored account overwritten: %s", got)
	}
}

func TestAuthCacheFillKeepsSaved(t *testing.T) {
	var c authCache
	saved := &authDoc{Username: "admin"}
	c.set(saved)
	if got := c.fill(nil); got != saved {
		t.Errorf("fill(nil) after set = %v, want saved doc", got)
	}
	if doc, _ := c.get(); doc != saved {
		t.Errorf("cache = %v after stale fill, want saved doc", doc)
	}
}

const (
	rightLogin = `{"username":"admin","password":"adminpass123"}`
	wrongLogin = `{"username":"admin","password":"wrongpass1"}`
)

func loginFrom(srv *Server, remote, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.RemoteAddr = remote
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func TestLoginLocksAfterFiveFailures(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	const remote = "198.51.100.7:1000"
	for i := range 5 {
		if w := loginFrom(srv, remote, wrongLogin); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong login %d: code %d, want 401", i+1, w.Code)
		}
	}
	if w := loginFrom(srv, remote, wrongLogin); w.Code != http.StatusTooManyRequests {
		t.Fatalf("6th wrong login: code %d, want 429", w.Code)
	}
	if w := loginFrom(srv, remote, rightLogin); w.Code != http.StatusTooManyRequests {
		t.Fatalf("right login during lock: code %d, want 429", w.Code)
	}
}

func fillHashSlots(srv *Server) {
	for range cap(srv.hashSlots) {
		srv.hashSlots <- struct{}{}
	}
}

func drainHashSlots(srv *Server) {
	for range cap(srv.hashSlots) {
		<-srv.hashSlots
	}
}

func errorText(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var resp errorResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error body %q: %v", w.Body.String(), err)
	}
	return resp.Error
}

func TestHashSlotsLimit(t *testing.T) {
	const busy = "too many login attempts at once, try later"
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	if got := cap(srv.hashSlots); got != 2 {
		t.Fatalf("hash slots = %d, want 2", got)
	}

	fillHashSlots(srv)
	// отказ по слоту не проверял пароль, поэтому не должен копиться в счетчике неудач
	for i := range 6 {
		w := loginFrom(srv, "198.51.100.7:1000", wrongLogin)
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("login %d with busy slots: code %d, want 429", i+1, w.Code)
		}
		if got := errorText(t, w); got != busy {
			t.Fatalf("login with busy slots: error %q, want %q", got, busy)
		}
	}
	if w := loginFrom(srv, "198.51.100.7:1000", rightLogin); w.Code != http.StatusTooManyRequests {
		t.Fatalf("right login with busy slots: code %d, want 429", w.Code)
	}
	drainHashSlots(srv)
	if w := loginFrom(srv, "198.51.100.7:1000", rightLogin); w.Code != http.StatusOK {
		t.Fatalf("login after drain: code %d, want 200", w.Code)
	}

	bare := newBareServer(&fakeStore{})
	fillHashSlots(bare)
	req := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(rightLogin))
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = "192.168.1.5:6410"
	w := httptest.NewRecorder()
	bare.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("setup with busy slots: code %d, want 429", w.Code)
	}
	if got := errorText(t, w); got != busy {
		t.Fatalf("setup with busy slots: error %q, want %q", got, busy)
	}
	if state := authState(t, bare, nil); state["configured"] != false {
		t.Errorf("configured = %v after busy setup, want false", state["configured"])
	}
}

func TestLoginLockExpiresAndSuccessResets(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	srv.loginFails.now = func() time.Time { return clock }
	const remote = "198.51.100.7:1000"

	for range 5 {
		loginFrom(srv, remote, wrongLogin)
	}
	if w := loginFrom(srv, remote, rightLogin); w.Code != http.StatusTooManyRequests {
		t.Fatalf("right login during lock: code %d, want 429", w.Code)
	}
	if w := loginFrom(srv, "198.51.100.8:1000", wrongLogin); w.Code != http.StatusUnauthorized {
		t.Fatalf("other address during lock: code %d, want 401", w.Code)
	}
	clock = clock.Add(61 * time.Second)
	if w := loginFrom(srv, remote, rightLogin); w.Code != http.StatusOK {
		t.Fatalf("right login after lock: code %d, want 200", w.Code)
	}

	for i, body := range []string{wrongLogin, wrongLogin, wrongLogin, wrongLogin, rightLogin, wrongLogin, wrongLogin, wrongLogin, wrongLogin} {
		if w := loginFrom(srv, remote, body); w.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d after success reset: code 429", i+1)
		}
	}

	clock = clock.Add(61 * time.Second)
	for i := range 4 {
		if w := loginFrom(srv, remote, wrongLogin); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong login %d in a new window: code %d, want 401", i+1, w.Code)
		}
	}
}

func TestLoginConcurrentAttemptsBounded(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	const n = 12
	// слотов на всех, чтобы лимит держал только счетчик попыток
	srv.hashSlots = make(chan struct{}, n)
	recs := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recs[i] = loginFrom(srv, "198.51.100.7:1000", wrongLogin)
		}()
	}
	wg.Wait()

	checked := 0
	for i, w := range recs {
		switch w.Code {
		case http.StatusUnauthorized:
			checked++
		case http.StatusTooManyRequests:
		default:
			t.Errorf("attempt %d: code %d, want 401 or 429", i, w.Code)
		}
	}
	if checked == 0 || checked > maxLoginFails {
		t.Errorf("password checks = %d, want 1..%d", checked, maxLoginFails)
	}
}

func TestLoginThrottleKeyBehindTrustedProxy(t *testing.T) {
	cases := []struct {
		name    string
		trusted bool
		remote  string
		xff     []string
		want    string
	}{
		{"last entry", true, "192.168.65.1:1000", []string{"203.0.113.9"}, "203.0.113.9"},
		{"spoofed left entries", true, "192.168.65.1:1000", []string{"6.6.6.6, 7.7.7.7, 203.0.113.9"}, "203.0.113.9"},
		{"spoofed earlier header", true, "192.168.65.1:1000", []string{"6.6.6.6", "203.0.113.9"}, "203.0.113.9"},
		{"garbage left entries", true, "192.168.65.1:1000", []string{"not-an-ip,, ;x, 203.0.113.9"}, "203.0.113.9"},
		{"garbage last entry", true, "192.168.65.1:1000", []string{"203.0.113.9, garbage"}, "<nil>"},
		{"public peer", true, "198.51.100.7:1000", []string{"203.0.113.9"}, "198.51.100.7"},
		{"proxy not trusted", false, "192.168.65.1:1000", []string{"203.0.113.9"}, "192.168.65.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newBareServer(&fakeStore{})
			srv.effective = func() config.App {
				cfg := testEffective()
				cfg.Web.TrustedProxy = tc.trusted
				return cfg
			}
			req := httptest.NewRequest("POST", "/api/auth/login", nil)
			req.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if got := srv.clientIP(req).String(); got != tc.want {
				t.Errorf("clientIP = %s, want %s", got, tc.want)
			}
		})
	}

	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true, TrustedProxy: true})
	for i := range 6 {
		req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(wrongLogin))
		req.RemoteAddr = "192.168.65.1:1000"
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d, 203.0.113.9", i))
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		want := http.StatusUnauthorized
		if i == 5 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("wrong login %d with rotated left XFF: code %d, want %d", i+1, w.Code, want)
		}
	}
}

func TestLoginThrottlePrunesExpiredKeys(t *testing.T) {
	th := newLoginThrottle()
	clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	th.now = func() time.Time { return clock }
	for range maxLoginFails {
		th.reserve("locked")
	}
	for i := range maxThrottleKeys {
		th.reserve(fmt.Sprintf("k%d", i))
	}
	clock = clock.Add(30 * time.Second)
	th.reserve("fresh")
	if got := len(th.fails); got != maxThrottleKeys+2 {
		t.Fatalf("keys inside the window = %d, want %d", got, maxThrottleKeys+2)
	}
	clock = clock.Add(31 * time.Second)
	th.reserve("next")
	// fresh еще в своем окне, остальные истекли
	if got := len(th.fails); got != 2 {
		t.Fatalf("keys after expiry = %d, want 2", got)
	}
	if !th.reserve("locked") {
		t.Error("expired lock still blocks")
	}
}

func historyWith(srv *Server, cookie *http.Cookie, host, referer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = host
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func logoutWith(srv *Server, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/auth/logout", nil)
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = "192.168.1.5:6410"
	req.Header.Set("Origin", "http://192.168.1.5:6410")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func authPost(srv *Server, path, host, origin, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = host
	req.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func testSecret(t *testing.T, srv *Server) []byte {
	t.Helper()
	doc, _ := srv.authCache.get()
	if doc == nil {
		t.Fatal("no account in cache")
	}
	secret, err := base64.RawURLEncoding.DecodeString(doc.Secret)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestLogoutRevokesAllSessions(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	a := sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	b := sessionCookie(loginFrom(srv, "192.168.1.11:1234", rightLogin))
	if a == nil || b == nil {
		t.Fatalf("login cookies: A %t, B %t", a != nil, b != nil)
	}

	w := logoutWith(srv, a)
	if w.Code != http.StatusOK {
		t.Fatalf("logout: code %d, want 200", w.Code)
	}
	if c := sessionCookie(w); c == nil || c.MaxAge >= 0 {
		t.Errorf("logout cookie set %t, want a clearing one", c != nil)
	}

	const host = "192.168.1.5:6410"
	if w := historyWith(srv, a, host, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("history with logged out cookie A: code %d, want 401", w.Code)
	}
	if w := historyWith(srv, b, host, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("history with other session B: code %d, want 401", w.Code)
	}
	fresh := sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	if fresh == nil {
		t.Fatal("login after logout: no cookie")
	}
	if w := historyWith(srv, fresh, host, ""); w.Code != http.StatusOK {
		t.Errorf("history with new login: code %d, want 200", w.Code)
	}
}

func TestLogoutWithoutSessionRevokesNothing(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	a := sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	if a == nil {
		t.Fatal("login: no cookie")
	}
	forged := &http.Cookie{Name: authCookieName, Value: signSession([]byte("not the secret"), "admin", time.Now().Add(time.Hour))}
	for i, c := range []*http.Cookie{nil, forged} {
		if w := logoutWith(srv, c); w.Code != http.StatusOK {
			t.Errorf("logout %d (none, forged): code %d, want 200", i, w.Code)
		}
	}
	if w := historyWith(srv, a, "192.168.1.5:6410", ""); w.Code != http.StatusOK {
		t.Errorf("history with cookie A after foreign logouts: code %d, want 200", w.Code)
	}
}

type failingSaveStore struct {
	fakeStore
	fail bool
}

func (f *failingSaveStore) SaveSetting(ctx context.Context, key string, value json.RawMessage) error {
	if f.fail {
		return errors.New("disk full")
	}
	return f.fakeStore.SaveSetting(ctx, key, value)
}

func TestLogoutSaveErrorKeepsSession(t *testing.T) {
	store := &failingSaveStore{}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)
	if w := authPost(srv, "/api/auth/setup", "192.168.1.5:6410", "http://192.168.1.5:6410", rightLogin); w.Code != http.StatusOK {
		t.Fatalf("setup: code %d", w.Code)
	}
	a := sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	if a == nil {
		t.Fatal("login: no cookie")
	}
	store.fail = true

	w := logoutWith(srv, a)
	if w.Code != http.StatusInternalServerError || errorText(t, w) != "auth error" {
		t.Errorf("logout with failing save: code %d body %s, want 500 auth error", w.Code, w.Body.String())
	}
	if c := sessionCookie(w); c != nil {
		t.Errorf("logout with failing save set cookie Max-Age %d, want none", c.MaxAge)
	}
	if w := historyWith(srv, a, "192.168.1.5:6410", ""); w.Code != http.StatusOK {
		t.Errorf("history after failed logout: code %d, want 200", w.Code)
	}
}

func TestSessionSlidingRenewal(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	secret := testSecret(t, srv)
	const host = "192.168.1.5:6410"
	const referer = "http://192.168.1.5:6410/"
	cookieAt := func(exp time.Time) *http.Cookie {
		return &http.Cookie{Name: authCookieName, Value: signSession(secret, "admin", exp)}
	}
	now := time.Now()
	old := cookieAt(now.Add(10 * 24 * time.Hour))

	w := historyWith(srv, old, host, referer)
	renewed := sessionCookie(w)
	if w.Code != http.StatusOK || renewed == nil {
		t.Fatalf("history with 10 day cookie: code %d, renewed %t, want 200 and a renewed cookie", w.Code, renewed != nil)
	}
	if !renewed.Expires.After(now.Add(29 * 24 * time.Hour)) {
		t.Errorf("renewed cookie expires %v, want after now+29d", renewed.Expires)
	}
	if w := historyWith(srv, renewed, host, ""); w.Code != http.StatusOK {
		t.Errorf("history with renewed cookie: code %d, want 200", w.Code)
	}

	w = historyWith(srv, cookieAt(now.Add(authSessionTTL-time.Minute)), host, referer)
	if w.Code != http.StatusOK || sessionCookie(w) != nil {
		t.Errorf("fresh cookie: code %d, Set-Cookie %t, want 200 and none", w.Code, sessionCookie(w) != nil)
	}

	w = historyWith(srv, old, host, "")
	if w.Code != http.StatusOK || sessionCookie(w) != nil {
		t.Errorf("old cookie without Origin and Referer: code %d, Set-Cookie %t, want 200 and none", w.Code, sessionCookie(w) != nil)
	}

	if w := historyWith(srv, cookieAt(now.Add(-time.Second)), host, referer); w.Code != http.StatusUnauthorized {
		t.Errorf("expired cookie: code %d, want 401", w.Code)
	}
}

func TestSessionCookieSecureFromScheme(t *testing.T) {
	web := config.Web{Listen: ":0", Enabled: true, AllowedHosts: []string{"my.keenetic.pro"}}
	srv := newConfiguredAuthServer(t, web)

	w := authPost(srv, "/api/auth/login", "192.168.1.200:6410", "https://my.keenetic.pro", rightLogin)
	if c := sessionCookie(w); w.Code != http.StatusOK || c == nil || !c.Secure {
		t.Errorf("login over https proxy: code %d, cookie set %t, Secure %t, want Secure", w.Code, c != nil, c != nil && c.Secure)
	}

	w = authPost(srv, "/api/auth/login", "192.168.1.5:6410", "http://192.168.1.5:6410", rightLogin)
	if c := sessionCookie(w); w.Code != http.StatusOK || c == nil || c.Secure {
		t.Errorf("login over http: code %d, cookie set %t, Secure %t, want not Secure", w.Code, c != nil, c != nil && c.Secure)
	}

	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(rightLogin))
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = "192.168.1.5:6410"
	req.Header.Set("Origin", "http://192.168.1.5:6410")
	req.Header.Set("Referer", "https://my.keenetic.pro/x")
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if c := sessionCookie(w); w.Code != http.StatusOK || c == nil || c.Secure {
		t.Errorf("login with http Origin and https Referer: code %d, cookie set %t, Secure %t, want not Secure", w.Code, c != nil, c != nil && c.Secure)
	}

	old := &http.Cookie{Name: authCookieName, Value: signSession(testSecret(t, srv), "admin", time.Now().Add(10*24*time.Hour))}
	w = historyWith(srv, old, "192.168.1.200:6410", "https://my.keenetic.pro/x")
	if c := sessionCookie(w); w.Code != http.StatusOK || c == nil || !c.Secure {
		t.Errorf("renewal over https proxy: code %d, cookie set %t, Secure %t, want Secure", w.Code, c != nil, c != nil && c.Secure)
	}

	bare := newBareServer(&fakeStore{})
	bare.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = web
		return cfg
	}
	w = authPost(bare, "/api/auth/setup", "192.168.1.200:6410", "https://my.keenetic.pro", rightLogin)
	if c := sessionCookie(w); w.Code != http.StatusOK || c == nil || !c.Secure {
		t.Errorf("setup over https proxy: code %d, cookie set %t, Secure %t, want Secure", w.Code, c != nil, c != nil && c.Secure)
	}
}

const (
	changeBody  = `{"current":"adminpass123","new":"newpass4567"}`
	wrongChange = `{"current":"wrongpass1","new":"newpass4567"}`
	newLogin    = `{"username":"admin","password":"newpass4567"}`
)

func passwordWith(srv *Server, cookie *http.Cookie, remote, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/auth/password", strings.NewReader(body))
	req.RemoteAddr = remote
	req.Host = "192.168.1.5:6410"
	req.Header.Set("Origin", "http://192.168.1.5:6410")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func sessionCookiesAll(w *httptest.ResponseRecorder) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == authCookieName {
			out = append(out, c)
		}
	}
	return out
}

func storedAuth(t *testing.T, srv *Server) string {
	t.Helper()
	raw, ok, err := srv.db.GetSetting(context.Background(), sqlite.SettingAuth)
	if err != nil || !ok {
		t.Fatalf("stored auth: ok %t, err %v", ok, err)
	}
	return string(raw)
}

func TestPasswordChangeRevokesOtherSessions(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	a := sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	b := sessionCookie(loginFrom(srv, "192.168.1.11:1234", rightLogin))
	if a == nil || b == nil {
		t.Fatalf("login cookies: A %t, B %t", a != nil, b != nil)
	}
	before, err := srv.readAuth(context.Background())
	if err != nil || before == nil {
		t.Fatalf("read auth before: %v, %v", before, err)
	}
	n := loggedCount("auth: password changed")

	w := passwordWith(srv, a, "192.168.1.10:1234", changeBody)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("change: code %d body %s, want 200 {\"ok\":true}", w.Code, w.Body.String())
	}
	cookies := sessionCookiesAll(w)
	if len(cookies) != 1 {
		t.Fatalf("change set %d session cookies, want 1", len(cookies))
	}
	fresh := cookies[0]
	if fresh.Value == a.Value || !fresh.HttpOnly || fresh.SameSite != http.SameSiteLaxMode || fresh.Path != "/" ||
		fresh.MaxAge != int(authSessionTTL.Seconds()) || fresh.Secure {
		t.Errorf("fresh cookie = %+v, want new value, HttpOnly, Lax, Path /, MaxAge %d, not Secure", fresh, int(authSessionTTL.Seconds()))
	}

	const host = "192.168.1.5:6410"
	if w := historyWith(srv, a, host, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("history with old cookie A: code %d, want 401", w.Code)
	}
	if w := historyWith(srv, b, host, ""); w.Code != http.StatusUnauthorized {
		t.Errorf("history with other session B: code %d, want 401", w.Code)
	}
	if w := historyWith(srv, fresh, host, ""); w.Code != http.StatusOK {
		t.Errorf("history with fresh cookie: code %d, want 200", w.Code)
	}

	after, err := srv.readAuth(context.Background())
	if err != nil || after == nil {
		t.Fatalf("read auth after: %v, %v", after, err)
	}
	if after.Salt == before.Salt || after.Hash == before.Hash || after.Secret == before.Secret {
		t.Errorf("salt, hash, secret changed: %t %t %t, want all", after.Salt != before.Salt, after.Hash != before.Hash, after.Secret != before.Secret)
	}
	if after.Username != before.Username || after.CreatedAt != before.CreatedAt {
		t.Errorf("username %q created %q, want %q %q", after.Username, after.CreatedAt, before.Username, before.CreatedAt)
	}

	if w := loginFrom(srv, "192.168.1.12:1234", rightLogin); w.Code != http.StatusUnauthorized {
		t.Errorf("login with old password: code %d, want 401", w.Code)
	}
	if w := loginFrom(srv, "192.168.1.12:1234", newLogin); w.Code != http.StatusOK {
		t.Errorf("login with new password: code %d, want 200", w.Code)
	}
	if got := loggedCount("auth: password changed") - n; got != 1 {
		t.Errorf("password changed log lines = %d, want 1", got)
	}
	for _, e := range logger.Snapshot(0) {
		if strings.Contains(e.Message, "newpass4567") || strings.Contains(e.Message, "adminpass123") {
			t.Errorf("password in log: %q", e.Message)
		}
	}
}

func TestPasswordChangeWrongCurrentCountsAsLoginFailure(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	a := sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	if a == nil {
		t.Fatal("login: no cookie")
	}
	raw := storedAuth(t, srv)
	const remote = "198.51.100.7:1000"
	for i := range 5 {
		w := passwordWith(srv, a, remote, wrongChange)
		if w.Code != http.StatusForbidden || errorText(t, w) != "invalid current password" {
			t.Fatalf("wrong change %d: code %d body %s, want 403 invalid current password", i+1, w.Code, w.Body.String())
		}
		if c := sessionCookie(w); c != nil {
			t.Errorf("wrong change %d set a session cookie", i+1)
		}
	}
	if w := passwordWith(srv, a, remote, wrongChange); w.Code != http.StatusTooManyRequests || errorText(t, w) != "too many attempts, try later" {
		t.Fatalf("6th wrong change: code %d body %s, want 429 too many attempts, try later", w.Code, w.Body.String())
	}
	if w := passwordWith(srv, a, remote, changeBody); w.Code != http.StatusTooManyRequests {
		t.Errorf("right change during lock: code %d, want 429", w.Code)
	}
	if w := loginFrom(srv, remote, rightLogin); w.Code != http.StatusTooManyRequests {
		t.Errorf("right login during lock: code %d, want 429", w.Code)
	}
	if w := historyWith(srv, a, "192.168.1.5:6410", ""); w.Code != http.StatusOK {
		t.Errorf("history with A after wrong changes: code %d, want 200", w.Code)
	}
	if got := storedAuth(t, srv); got != raw {
		t.Errorf("stored auth changed by rejected changes")
	}

	srv = newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	a = sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	if a == nil {
		t.Fatal("login on second server: no cookie")
	}
	for i := range 4 {
		if w := passwordWith(srv, a, remote, wrongChange); w.Code != http.StatusForbidden {
			t.Fatalf("second server wrong change %d: code %d, want 403", i+1, w.Code)
		}
	}
	w := passwordWith(srv, a, remote, changeBody)
	fresh := sessionCookie(w)
	if w.Code != http.StatusOK || fresh == nil {
		t.Fatalf("right change after 4 failures: code %d, cookie %t, want 200 with cookie", w.Code, fresh != nil)
	}
	for i := range 4 {
		if w := passwordWith(srv, fresh, remote, wrongChange); w.Code != http.StatusForbidden {
			t.Errorf("wrong change %d after success: code %d, want 403", i+1, w.Code)
		}
	}
}

func TestPasswordChangeRejectsBeforeHashing(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	a := sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	if a == nil {
		t.Fatal("login: no cookie")
	}
	raw := storedAuth(t, srv)
	const remote = "198.51.100.9:1000"
	forged := &http.Cookie{Name: authCookieName, Value: signSession([]byte("not the secret"), "admin", time.Now().Add(time.Hour))}
	cases := []struct {
		name   string
		cookie *http.Cookie
		body   string
		slots  bool
		code   int
		text   string
	}{
		{"no cookie", nil, changeBody, false, http.StatusUnauthorized, "unauthorized"},
		{"forged cookie", forged, changeBody, false, http.StatusUnauthorized, "unauthorized"},
		{"bad body", a, `{bad`, false, http.StatusBadRequest, "invalid body"},
		{"short new", a, `{"current":"adminpass123","new":"short7x"}`, false, http.StatusBadRequest, "password must be at least 8 chars"},
		{"slots busy", a, changeBody, true, http.StatusTooManyRequests, "too many login attempts at once, try later"},
	}
	for _, tc := range cases {
		if tc.slots {
			fillHashSlots(srv)
		}
		w := passwordWith(srv, tc.cookie, remote, tc.body)
		if tc.slots {
			drainHashSlots(srv)
		}
		if w.Code != tc.code || errorText(t, w) != tc.text {
			t.Errorf("%s: code %d body %s, want %d %s", tc.name, w.Code, w.Body.String(), tc.code, tc.text)
		}
	}
	if got := storedAuth(t, srv); got != raw {
		t.Errorf("stored auth changed by rejected requests")
	}
	if w := passwordWith(srv, a, remote, wrongChange); w.Code != http.StatusForbidden {
		t.Errorf("wrong change after rejects: code %d, want 403", w.Code)
	}
}

func TestPasswordChangeSaveErrorKeepsPassword(t *testing.T) {
	store := &failingSaveStore{}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)
	if w := authPost(srv, "/api/auth/setup", "192.168.1.5:6410", "http://192.168.1.5:6410", rightLogin); w.Code != http.StatusOK {
		t.Fatalf("setup: code %d", w.Code)
	}
	a := sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	if a == nil {
		t.Fatal("login: no cookie")
	}
	store.fail = true

	w := passwordWith(srv, a, "192.168.1.10:1234", changeBody)
	if w.Code != http.StatusInternalServerError || errorText(t, w) != "auth error" {
		t.Errorf("change with failing save: code %d body %s, want 500 auth error", w.Code, w.Body.String())
	}
	if c := sessionCookie(w); c != nil {
		t.Errorf("change with failing save set a session cookie")
	}
	if w := historyWith(srv, a, "192.168.1.5:6410", ""); w.Code != http.StatusOK {
		t.Errorf("history after failed change: code %d, want 200", w.Code)
	}
	if w := loginFrom(srv, "192.168.1.10:1234", rightLogin); w.Code != http.StatusOK {
		t.Errorf("login with old password after failed change: code %d, want 200", w.Code)
	}
	if w := loginFrom(srv, "192.168.1.10:1234", newLogin); w.Code != http.StatusUnauthorized {
		t.Errorf("login with new password after failed change: code %d, want 401", w.Code)
	}
}

func TestPasswordChangeCookieSecure(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true, AllowedHosts: []string{"my.keenetic.pro"}})
	a := loginCookie(t, srv)

	req := httptest.NewRequest("POST", "/api/auth/password", strings.NewReader(changeBody))
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = "192.168.1.200:6410"
	req.Header.Set("Origin", "https://my.keenetic.pro")
	req.AddCookie(a)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("change over https proxy: code %d body %s, want 200", w.Code, w.Body.String())
	}
	cookies := sessionCookiesAll(w)
	if len(cookies) != 1 || !cookies[0].Secure {
		t.Errorf("change over https proxy: %d session cookies, want 1 Secure", len(cookies))
	}
}

func TestPasswordChangeConcurrentOneWins(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	a := sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	if a == nil {
		t.Fatal("login: no cookie")
	}
	passwords := []string{"firstpass111", "secondpass222"}
	codes := make([]int, len(passwords))
	var wg sync.WaitGroup
	for i, p := range passwords {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := fmt.Sprintf(`{"current":"adminpass123","new":%q}`, p)
			codes[i] = passwordWith(srv, a, "192.168.1.10:1234", body).Code
		}()
	}
	wg.Wait()

	winner, loser := -1, -1
	for i, c := range codes {
		switch c {
		case http.StatusOK:
			winner = i
		case http.StatusUnauthorized:
			loser = i
		}
	}
	if winner < 0 || loser < 0 {
		t.Fatalf("codes = %v, want one 200 and one 401", codes)
	}
	loginBody := func(p string) string { return fmt.Sprintf(`{"username":"admin","password":%q}`, p) }
	if w := loginFrom(srv, "192.168.1.12:1234", loginBody(passwords[loser])); w.Code != http.StatusUnauthorized {
		t.Errorf("login with loser password: code %d, want 401", w.Code)
	}
	if w := loginFrom(srv, "192.168.1.12:1234", rightLogin); w.Code != http.StatusUnauthorized {
		t.Errorf("login with old password: code %d, want 401", w.Code)
	}
	if w := loginFrom(srv, "192.168.1.12:1234", loginBody(passwords[winner])); w.Code != http.StatusOK {
		t.Errorf("login with winner password: code %d, want 200", w.Code)
	}
}
