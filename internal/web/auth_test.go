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
	"strconv"
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
	if key == sqlite.SettingAuth {
		c.getSettingCalls++
	}
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
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
	req.Host = "192.168.1.5:6410"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate setup: expected 409, got %d", w.Code)
	}

	loginBody := []byte(`{"username":"testuser","password":"testpass123"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("login with wrong password: expected 401, got %d", w.Code)
	}

	loginBody := []byte(`{"username":"admin","password":"newpass123"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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
			req.Header.Set("X-Ripperr", "1")
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
			req.Header.Set("X-Ripperr", "1")
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

func csrfRequest(srv *Server, method, path, host, origin, proof, fetchSite, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if proof != "" {
		req.Header.Set("X-Ripperr", proof)
	}
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func TestCSRFProofRequired(t *testing.T) {
	const lanHost, lanOrigin, proxyHost = "192.168.1.5:6410", "http://192.168.1.5:6410", "192.168.1.105"
	cases := []struct {
		name      string
		host      string
		origin    string
		proof     string
		fetchSite string
		want      int
	}{
		{"no header", lanHost, lanOrigin, "", "", http.StatusForbidden},
		{"header 1", lanHost, lanOrigin, "1", "", http.StatusNoContent},
		{"header 0", lanHost, lanOrigin, "0", "", http.StatusForbidden},
		{"same-site", lanHost, lanOrigin, "1", "same-site", http.StatusForbidden},
		{"cross-site", lanHost, lanOrigin, "1", "cross-site", http.StatusForbidden},
		{"same-origin", lanHost, lanOrigin, "1", "same-origin", http.StatusNoContent},
		{"none", lanHost, lanOrigin, "1", "none", http.StatusNoContent},
		{"proxy no header", proxyHost, "", "", "", http.StatusForbidden},
		{"proxy header 1", proxyHost, "", "1", "", http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
			cookie := loginCookie(t, srv)
			w := csrfRequest(srv, "DELETE", "/api/ignore/abc", tc.host, tc.origin, tc.proof, tc.fetchSite, "", cookie)
			if w.Code != tc.want {
				t.Fatalf("code %d, want %d", w.Code, tc.want)
			}
			if tc.want == http.StatusForbidden {
				if got := errorText(t, w); got != "cross-origin request" {
					t.Errorf("error %q, want cross-origin request", got)
				}
			}
		})
	}

	t.Run("login", func(t *testing.T) {
		srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
		w := csrfRequest(srv, "POST", "/api/auth/login", lanHost, lanOrigin, "", "", rightLogin, nil)
		if w.Code != http.StatusForbidden || sessionCookie(w) != nil {
			t.Fatalf("login without header: code %d, cookie %v, want 403 and no cookie", w.Code, sessionCookie(w))
		}
		if w := csrfRequest(srv, "POST", "/api/auth/login", lanHost, lanOrigin, "1", "", rightLogin, nil); w.Code != http.StatusOK {
			t.Fatalf("login with header: code %d, want 200", w.Code)
		}
	})

	t.Run("logout", func(t *testing.T) {
		srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
		cookie := loginCookie(t, srv)
		if w := csrfRequest(srv, "POST", "/api/auth/logout", lanHost, lanOrigin, "", "", "", cookie); w.Code != http.StatusForbidden {
			t.Fatalf("logout without header: code %d, want 403", w.Code)
		}
		if w := historyWith(srv, cookie, lanHost, ""); w.Code != http.StatusOK {
			t.Fatalf("history after refused logout: code %d, want 200", w.Code)
		}
	})

	t.Run("setup", func(t *testing.T) {
		bare := newBareServer(&fakeStore{})
		if w := csrfRequest(bare, "POST", "/api/auth/setup", lanHost, "", "", "", rightLogin, nil); w.Code != http.StatusForbidden {
			t.Fatalf("setup without header: code %d, want 403", w.Code)
		}
	})

	t.Run("safe and non-api", func(t *testing.T) {
		srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
		cookie := loginCookie(t, srv)
		if w := csrfRequest(srv, "GET", "/api/history", lanHost, "", "", "", "", cookie); w.Code != http.StatusOK {
			t.Errorf("GET /api/history without header: code %d, want 200", w.Code)
		}
		if w := csrfRequest(srv, "GET", "/metrics", lanHost, "", "", "", "", nil); w.Code == http.StatusForbidden {
			t.Errorf("GET /metrics without header: code 403")
		}
		w := csrfRequest(srv, "OPTIONS", "/api/run", lanHost, "http://evil.example", "", "", "", cookie)
		if w.Code == http.StatusForbidden {
			t.Errorf("OPTIONS /api/run: code 403")
		}
		for name := range w.Header() {
			if strings.HasPrefix(name, "Access-Control-Allow-") {
				t.Errorf("OPTIONS /api/run: unexpected header %s", name)
			}
		}
	})
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
	if all := sessionCookiesAll(w); len(all) > 0 {
		return all[0]
	}
	// живой нет: отдаем удаление, его проверяют тесты выхода
	for _, c := range w.Result().Cookies() {
		if c.Name == authCookieName || c.Name == hostCookiePrefix+authCookieName {
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
		req.Header.Set("X-Ripperr", "1")
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
		{"192.168.1.105", nil, http.StatusForbidden},
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
			req.Header.Set("X-Ripperr", "1")
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
			req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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

func TestLoginReportsAttemptsLeft(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	const remote = "198.51.100.8:1000"
	for i := range maxLoginFails {
		w := loginFrom(srv, remote, wrongLogin)
		var resp struct {
			Error        string `json:"error"`
			AttemptsLeft *int   `json:"attempts_left"`
			RetryAfter   int    `json:"retry_after"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong login %d: code %d body %s", i+1, w.Code, w.Body.String())
		}
		if want := maxLoginFails - i - 1; resp.AttemptsLeft == nil || *resp.AttemptsLeft != want {
			t.Fatalf("wrong login %d: attempts_left %v, want %d", i+1, resp.AttemptsLeft, want)
		}
		if locked := i == maxLoginFails-1; (resp.RetryAfter > 0) != locked {
			t.Fatalf("wrong login %d: retry_after %d, locked %v", i+1, resp.RetryAfter, locked)
		}
	}
	w := loginFrom(srv, remote, wrongLogin)
	var locked struct {
		RetryAfter int `json:"retry_after"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &locked); err != nil || w.Code != http.StatusTooManyRequests {
		t.Fatalf("locked login: code %d body %s", w.Code, w.Body.String())
	}
	if locked.RetryAfter < 1 || locked.RetryAfter > int(loginLockWindow.Seconds()) {
		t.Fatalf("retry_after %d, want 1..%d", locked.RetryAfter, int(loginLockWindow.Seconds()))
	}
	if h := w.Header().Get("Retry-After"); h != strconv.Itoa(locked.RetryAfter) {
		t.Fatalf("Retry-After header %q, want %d", h, locked.RetryAfter)
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
	req.Header.Set("X-Ripperr", "1")
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
	clock = clock.Add(loginLockWindow + time.Second)
	if w := loginFrom(srv, remote, rightLogin); w.Code != http.StatusOK {
		t.Fatalf("right login after lock: code %d, want 200", w.Code)
	}

	for i, body := range []string{wrongLogin, wrongLogin, wrongLogin, wrongLogin, rightLogin, wrongLogin, wrongLogin, wrongLogin, wrongLogin} {
		if w := loginFrom(srv, remote, body); w.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d after success reset: code 429", i+1)
		}
	}

	clock = clock.Add(loginLockWindow + time.Second)
	for i := range 4 {
		if w := loginFrom(srv, remote, wrongLogin); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong login %d in a new window: code %d, want 401", i+1, w.Code)
		}
	}
}

func TestLoginLockLastsADay(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	clock := t0
	srv.loginFails.now = func() time.Time { return clock }
	const remote = "198.51.100.20:1000"
	retryAfter := func(w *httptest.ResponseRecorder) int {
		t.Helper()
		var resp struct {
			RetryAfter int `json:"retry_after"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode body %q: %v", w.Body.String(), err)
		}
		return resp.RetryAfter
	}

	for i := range maxLoginFails {
		w := loginFrom(srv, remote, wrongLogin)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong login %d: code %d, want 401", i+1, w.Code)
		}
		if i == maxLoginFails-1 {
			if got := retryAfter(w); got != 86400 {
				t.Fatalf("5th wrong login: retry_after %d, want 86400", got)
			}
		}
	}
	w := loginFrom(srv, remote, wrongLogin)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("6th wrong login: code %d, want 429", w.Code)
	}
	if got := retryAfter(w); got != 86400 {
		t.Fatalf("6th wrong login: retry_after %d, want 86400", got)
	}
	if h := w.Header().Get("Retry-After"); h != "86400" {
		t.Fatalf("Retry-After header %q, want 86400", h)
	}
	if w := loginFrom(srv, "198.51.100.21:1000", wrongLogin); w.Code != http.StatusUnauthorized {
		t.Fatalf("other address during lock: code %d, want 401", w.Code)
	}

	clock = t0.Add(23*time.Hour + 59*time.Minute)
	w = loginFrom(srv, remote, rightLogin)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("right login a minute before unlock: code %d, want 429", w.Code)
	}
	if got := retryAfter(w); got != 60 {
		t.Fatalf("right login a minute before unlock: retry_after %d, want 60", got)
	}

	clock = t0.Add(24 * time.Hour)
	if w := loginFrom(srv, remote, rightLogin); w.Code != http.StatusOK {
		t.Fatalf("right login after a day: code %d, want 200", w.Code)
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
		req.Header.Set("X-Ripperr", "1")
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
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	clock := t0
	th.now = func() time.Time { return clock }
	for range maxLoginFails {
		th.reserve("locked")
	}
	for i := range maxThrottleKeys - 2 {
		th.reserve(fmt.Sprintf("k%d", i))
	}
	if got := len(th.fails); got != maxThrottleKeys-1 {
		t.Fatalf("keys after fill = %d, want %d", got, maxThrottleKeys-1)
	}
	clock = t0.Add(12 * time.Hour)
	for range maxLoginFails {
		th.reserve("late")
	}
	if got := len(th.fails); got != maxThrottleKeys {
		t.Fatalf("keys after late = %d, want %d", got, maxThrottleKeys)
	}
	if th.reserve("fresh") {
		t.Fatal("new key accepted on a full table")
	}
	if got := len(th.fails); got != maxThrottleKeys {
		t.Fatalf("keys after refused fresh = %d, want %d", got, maxThrottleKeys)
	}
	if left, wait := th.status("fresh"); left != 0 || wait != loginLockWindow {
		t.Fatalf("status(fresh) = (%d, %v), want (0, %v)", left, wait, loginLockWindow)
	}
	if !th.reserve("k0") {
		t.Fatal("existing key refused on a full table")
	}

	clock = t0.Add(24*time.Hour + time.Second)
	if !th.reserve("k1") {
		t.Fatal("existing key with an expired window refused on a full table")
	}
	if got := len(th.fails); got != maxThrottleKeys {
		t.Fatalf("keys after k1 reset = %d, want %d", got, maxThrottleKeys)
	}
	if !th.reserve("next") {
		t.Fatal("new key refused after expiry")
	}
	// late еще заблокирован, k1 сброшен в новом окне, остальные истекли
	if got := len(th.fails); got != 3 {
		t.Fatalf("keys after expiry = %d, want 3", got)
	}
	if th.reserve("late") {
		t.Fatal("late lock expired too early")
	}
	if _, wait := th.status("late"); wait != 12*time.Hour-time.Second {
		t.Fatalf("status(late) wait = %v, want %v", wait, 12*time.Hour-time.Second)
	}

	clock = t0.Add(36 * time.Hour)
	if !th.reserve("late") {
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
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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
	req.Header.Set("X-Ripperr", "1")
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

func TestSessionCookieSecureBehindStrippingProxy(t *testing.T) {
	web := config.Web{Listen: ":0", Enabled: true, AllowedHosts: []string{"track.example.net"}}
	srv := newConfiguredAuthServer(t, web)

	// так приходит запрос через https-прокси keenetic: Host это адрес бэкенда без порта, Origin и Referer срезаны
	w := authPost(srv, "/api/auth/login", "192.168.1.105", "", rightLogin)
	if c := sessionCookie(w); w.Code != http.StatusOK || c == nil || !c.Secure {
		t.Errorf("login behind stripping proxy: code %d, cookie set %t, Secure %t, want Secure", w.Code, c != nil, c != nil && c.Secure)
	}

	old := &http.Cookie{Name: authCookieName, Value: signSession(testSecret(t, srv), "admin", time.Now().Add(10*24*time.Hour))}
	w = historyWith(srv, old, "192.168.1.105", "")
	if c := sessionCookie(w); w.Code != http.StatusOK || c == nil || !c.Secure {
		t.Errorf("renewal behind stripping proxy: code %d, cookie set %t, Secure %t, want Secure", w.Code, c != nil, c != nil && c.Secure)
	}

	for _, host := range []string{"192.168.1.105:6410", "ripperr.local", "[::1]:6410"} {
		if w := historyWith(srv, old, host, ""); w.Code != http.StatusOK || sessionCookie(w) != nil {
			t.Errorf("host %s without Origin and Referer: code %d, Set-Cookie %t, want 200 and none", host, w.Code, sessionCookie(w) != nil)
		}
	}
}

const (
	changeBody  = `{"current":"adminpass123","new":"newpass4567"}`
	wrongChange = `{"current":"wrongpass1","new":"newpass4567"}`
	newLogin    = `{"username":"admin","password":"newpass4567"}`
)

func passwordWith(srv *Server, cookie *http.Cookie, remote, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/auth/password", strings.NewReader(body))
	req.Header.Set("X-Ripperr", "1")
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
		if (c.Name == authCookieName || c.Name == hostCookiePrefix+authCookieName) && c.MaxAge >= 0 {
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
	req.Header.Set("X-Ripperr", "1")
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

const (
	testDeviceCookie = "ripperr_device"
	nat              = "192.168.65.1:1000"
)

func deviceCookiesAll(w *httptest.ResponseRecorder) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range w.Result().Cookies() {
		if (c.Name == testDeviceCookie || c.Name == hostCookiePrefix+testDeviceCookie) && c.MaxAge >= 0 {
			out = append(out, c)
		}
	}
	return out
}

func deviceCookieOf(w *httptest.ResponseRecorder) *http.Cookie {
	if all := deviceCookiesAll(w); len(all) > 0 {
		return all[0]
	}
	return nil
}

func deviceIDPart(value string) string {
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func addCookies(req *http.Request, cookies []*http.Cookie) {
	for _, c := range cookies {
		if c != nil {
			req.AddCookie(c)
		}
	}
}

func loginWith(srv *Server, remote, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("X-Ripperr", "1")
	req.RemoteAddr = remote
	req.Host = "192.168.1.5:6410"
	req.Header.Set("Origin", "http://192.168.1.5:6410")
	addCookies(req, cookies)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func apiWith(srv *Server, referer string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = "192.168.1.5:6410"
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	addCookies(req, cookies)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func passwordWithCookies(srv *Server, remote, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/auth/password", strings.NewReader(body))
	req.Header.Set("X-Ripperr", "1")
	req.RemoteAddr = remote
	req.Host = "192.168.1.5:6410"
	req.Header.Set("Origin", "http://192.168.1.5:6410")
	addCookies(req, cookies)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func storedAuthMap(t *testing.T, srv *Server) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(storedAuth(t, srv)), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func storedDeviceKey(t *testing.T, srv *Server) []byte {
	t.Helper()
	v, ok := storedAuthMap(t, srv)["device_key"].(string)
	if !ok || v == "" {
		t.Fatalf("stored auth has no device_key")
	}
	key, err := base64.RawURLEncoding.DecodeString(v)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func lockAddress(t *testing.T, srv *Server, remote string) {
	t.Helper()
	for i := range maxLoginFails {
		if w := loginWith(srv, remote, wrongLogin); w.Code != http.StatusUnauthorized {
			t.Fatalf("lock %s: wrong login %d code %d, want 401", remote, i+1, w.Code)
		}
	}
	if w := loginWith(srv, remote, wrongLogin); w.Code != http.StatusTooManyRequests {
		t.Fatalf("lock %s: 6th wrong login code %d, want 429", remote, w.Code)
	}
}

func loginRespOf(t *testing.T, w *httptest.ResponseRecorder) loginErrorResp {
	t.Helper()
	var resp loginErrorResp
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login body %q: %v", w.Body.String(), err)
	}
	return resp
}

func wantAttemptsLeft(t *testing.T, w *httptest.ResponseRecorder, step string, want int) loginErrorResp {
	t.Helper()
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("%s: code %d body %s, want 401", step, w.Code, w.Body.String())
	}
	resp := loginRespOf(t, w)
	if resp.AttemptsLeft == nil || *resp.AttemptsLeft != want {
		t.Fatalf("%s: attempts_left %v, want %d", step, resp.AttemptsLeft, want)
	}
	return resp
}

func TestLoginIssuesDeviceCookie(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true, AllowedHosts: []string{"my.keenetic.pro"}})

	w := loginWith(srv, nat, rightLogin)
	all := deviceCookiesAll(w)
	if w.Code != http.StatusOK || len(all) != 1 {
		t.Fatalf("login: code %d, %d device cookies, want 200 and 1", w.Code, len(all))
	}
	d := all[0]
	if n := len(strings.Split(d.Value, ".")); n != 3 {
		t.Errorf("device cookie has %d parts, want 3", n)
	}
	if !d.HttpOnly || d.SameSite != http.SameSiteLaxMode || d.Path != "/" || d.MaxAge != 34560000 || d.Secure {
		t.Errorf("device cookie attrs HttpOnly %t SameSite %v Path %q MaxAge %d Secure %t", d.HttpOnly, d.SameSite, d.Path, d.MaxAge, d.Secure)
	}
	if diff := time.Until(d.Expires) - 400*24*time.Hour; diff > time.Minute || diff < -time.Minute {
		t.Errorf("device cookie expires %v, want about now+400d", d.Expires)
	}

	w = authPost(srv, "/api/auth/login", "192.168.1.200:6410", "https://my.keenetic.pro", rightLogin)
	if d := deviceCookieOf(w); w.Code != http.StatusOK || d == nil || !d.Secure {
		t.Errorf("https login: code %d, device cookie %v, want 200 with a Secure one", w.Code, d)
	}

	w = loginWith(srv, nat, wrongLogin)
	if w.Code != http.StatusUnauthorized || deviceCookieOf(w) != nil {
		t.Errorf("wrong login: code %d, device cookie %t, want 401 without one", w.Code, deviceCookieOf(w) != nil)
	}

	d1 := deviceCookieOf(loginWith(srv, nat, rightLogin))
	d2 := deviceCookieOf(loginWith(srv, nat, rightLogin))
	if d1 == nil || d2 == nil || deviceIDPart(d1.Value) == deviceIDPart(d2.Value) {
		t.Errorf("two logins: device cookies %v, %v, want two different ids", d1, d2)
	}

	bare := newBareServer(&fakeStore{})
	w = authPost(bare, "/api/auth/setup", "192.168.1.5:6410", "http://192.168.1.5:6410", rightLogin)
	if w.Code != http.StatusOK || len(deviceCookiesAll(w)) != 1 {
		t.Fatalf("setup: code %d, %d device cookies, want 200 and 1", w.Code, len(deviceCookiesAll(w)))
	}
	if key := storedDeviceKey(t, bare); len(key) != 32 {
		t.Errorf("device_key %d bytes, want 32", len(key))
	}
}

func TestDeviceKeyCreatedForOldAccount(t *testing.T) {
	strip := func(srv *Server) map[string]any {
		t.Helper()
		m := storedAuthMap(t, srv)
		delete(m, "device_key")
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.db.SaveSetting(context.Background(), sqlite.SettingAuth, raw); err != nil {
			t.Fatal(err)
		}
		var doc authDoc
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		srv.authCache.set(&doc)
		return m
	}

	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	a := sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	if a == nil {
		t.Fatal("login: no session cookie")
	}
	old := strip(srv)

	w := loginWith(srv, nat, rightLogin)
	if w.Code != http.StatusOK || len(deviceCookiesAll(w)) != 1 {
		t.Fatalf("login of old account: code %d, %d device cookies, want 200 and 1", w.Code, len(deviceCookiesAll(w)))
	}
	now := storedAuthMap(t, srv)
	for _, k := range []string{"username", "salt", "hash", "secret", "created_at"} {
		if now[k] != old[k] {
			t.Errorf("stored %s changed: %v -> %v", k, old[k], now[k])
		}
	}
	storedDeviceKey(t, srv)
	if w := historyWith(srv, a, "192.168.1.5:6410", ""); w.Code != http.StatusOK {
		t.Errorf("history with session A after key creation: code %d, want 200", w.Code)
	}

	strip(srv)
	devs := make([]*http.Cookie, 2)
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i, remote := range []string{"192.168.1.20:1", "192.168.1.21:1"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := loginWith(srv, remote, rightLogin)
			codes[i], devs[i] = w.Code, deviceCookieOf(w)
		}()
	}
	wg.Wait()
	for i := range devs {
		if codes[i] != http.StatusOK || devs[i] == nil {
			t.Fatalf("concurrent login %d: code %d, device cookie %t, want 200 with one", i, codes[i], devs[i] != nil)
		}
	}
	lockAddress(t, srv, nat)
	for i, d := range devs {
		if w := loginWith(srv, nat, rightLogin, d); w.Code != http.StatusOK {
			t.Errorf("trusted login %d after concurrent key creation: code %d, want 200", i, w.Code)
		}
	}

	store := &failingSaveStore{}
	srv = New(store, func() {}, testEffective, ":0", runner.NewState(), nil)
	if w := authPost(srv, "/api/auth/setup", "192.168.1.5:6410", "http://192.168.1.5:6410", rightLogin); w.Code != http.StatusOK {
		t.Fatalf("setup: code %d", w.Code)
	}
	strip(srv)
	store.fail = true
	w = loginWith(srv, nat, rightLogin)
	s := sessionCookie(w)
	if w.Code != http.StatusOK || s == nil || deviceCookieOf(w) != nil {
		t.Fatalf("login with failing save: code %d, session %t, device %t, want 200 with session only", w.Code, s != nil, deviceCookieOf(w) != nil)
	}
	if w := apiWith(srv, "http://192.168.1.5:6410/", s); w.Code != http.StatusOK || deviceCookieOf(w) != nil {
		t.Errorf("api with failing save: code %d, device %t, want 200 without one", w.Code, deviceCookieOf(w) != nil)
	}
}

func TestTrustedDeviceSurvivesAddressLock(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	w := loginWith(srv, nat, rightLogin)
	d, s := deviceCookieOf(w), sessionCookie(w)
	if d == nil || s == nil {
		t.Fatalf("login: device %t, session %t, want both", d != nil, s != nil)
	}
	if w := logoutWith(srv, s); w.Code != http.StatusOK || deviceCookieOf(w) != nil {
		t.Fatalf("logout: code %d, device cookie set %t, want 200 without one", w.Code, deviceCookieOf(w) != nil)
	}
	lockAddress(t, srv, nat)

	w = loginWith(srv, nat, rightLogin, d)
	d2 := deviceCookieOf(w)
	if w.Code != http.StatusOK || d2 == nil {
		t.Fatalf("trusted login on locked address: code %d, device %t, want 200 with a new one", w.Code, d2 != nil)
	}
	wantAttemptsLeft(t, loginWith(srv, nat, wrongLogin, d2), "trusted wrong login", maxLoginFails-1)
	if w := loginWith(srv, nat, rightLogin); w.Code != http.StatusTooManyRequests {
		t.Errorf("untrusted login after trusted success: code %d, want 429 (address lock kept)", w.Code)
	}
	if w := loginWith(srv, nat, rightLogin, d); w.Code != http.StatusOK {
		t.Errorf("first device cookie after relogin: code %d, want 200", w.Code)
	}
}

func TestDeviceLockIsPerDevice(t *testing.T) {
	const (
		devLine  = "auth: sign-in locked after failed attempts (device), client 192.168.65.4"
		addrLine = "auth: sign-in locked after failed attempts (address), client 198.51.100.30"
	)
	// журнал общий на весь процесс: при -count=2 в нем уже лежат строки прошлого прогона
	countLocks := func() (dev, addr int) {
		for _, e := range logger.Snapshot(0) {
			switch e.Message {
			case devLine:
				dev++
			case addrLine:
				addr++
			}
		}
		return dev, addr
	}
	devBase, addrBase := countLocks()
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	const n4 = "192.168.65.4:1000"
	a := deviceCookieOf(loginWith(srv, n4, rightLogin))
	b := deviceCookieOf(loginWith(srv, n4, rightLogin))
	if a == nil || b == nil {
		t.Fatalf("login: device A %t, B %t", a != nil, b != nil)
	}
	for i := range maxLoginFails {
		resp := wantAttemptsLeft(t, loginWith(srv, n4, wrongLogin, a), fmt.Sprintf("device A wrong login %d", i+1), maxLoginFails-i-1)
		if i == maxLoginFails-1 && (resp.RetryAfter < 86399 || resp.RetryAfter > 86400) {
			t.Fatalf("5th wrong login: retry_after %d, want 86399..86400", resp.RetryAfter)
		}
	}
	w := loginWith(srv, n4, wrongLogin, a)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("6th wrong login with A: code %d, want 429", w.Code)
	}
	resp := loginRespOf(t, w)
	if resp.RetryAfter < 86398 || resp.RetryAfter > 86400 || w.Header().Get("Retry-After") != strconv.Itoa(resp.RetryAfter) {
		t.Fatalf("6th wrong login: retry_after %d header %q, want 86398..86400 and equal", resp.RetryAfter, w.Header().Get("Retry-After"))
	}
	if w := loginWith(srv, n4, rightLogin, a); w.Code != http.StatusTooManyRequests {
		t.Errorf("right login with locked A: code %d, want 429", w.Code)
	}
	if w := loginWith(srv, n4, rightLogin, b); w.Code != http.StatusOK {
		t.Errorf("right login with B: code %d, want 200", w.Code)
	}
	if w := loginWith(srv, n4, rightLogin); w.Code != http.StatusOK {
		t.Errorf("right login without device: code %d, want 200", w.Code)
	}
	lockAddress(t, srv, "198.51.100.30:1000")

	devN, addrN := countLocks()
	devN, addrN = devN-devBase, addrN-addrBase
	for _, e := range logger.Snapshot(0) {
		for _, secret := range []string{"wrongpass1", "adminpass123", a.Value} {
			if strings.Contains(e.Message, secret) {
				t.Errorf("log line %q leaks %q", e.Message, secret)
			}
		}
	}
	if devN != 1 || addrN != 1 {
		t.Errorf("lock log lines: device %d, address %d, want 1 and 1", devN, addrN)
	}
}

func TestForgedDeviceCookieUntrusted(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	key := storedDeviceKey(t, srv)
	s := sessionCookie(loginFrom(srv, "192.168.1.10:1234", rightLogin))
	if s == nil {
		t.Fatal("login: no session cookie")
	}
	const remote = "198.51.100.31:1000"
	now := time.Now()
	forged := []string{
		signSession([]byte("not the device key"), "fakeid", now.Add(time.Hour)),
		signSession(key, "expired", now.Add(-time.Second)),
		s.Value,
		signSession(testSecret(t, srv), "fakeid", now.Add(time.Hour)),
		"garbage",
	}
	for i, v := range forged {
		w := loginWith(srv, remote, wrongLogin, &http.Cookie{Name: testDeviceCookie, Value: v})
		wantAttemptsLeft(t, w, fmt.Sprintf("forged cookie %d", i), maxLoginFails-i-1)
	}
	if w := loginWith(srv, remote, wrongLogin, &http.Cookie{Name: testDeviceCookie, Value: "a.b"}); w.Code != http.StatusTooManyRequests {
		t.Errorf("6th forged attempt: code %d, want 429", w.Code)
	}
	if w := loginWith(srv, remote, rightLogin, &http.Cookie{Name: testDeviceCookie, Value: forged[0]}); w.Code != http.StatusTooManyRequests {
		t.Errorf("right login with forged cookie on locked address: code %d, want 429", w.Code)
	}
	d := deviceCookieOf(loginWith(srv, nat, rightLogin))
	if d == nil {
		t.Fatal("login: no device cookie")
	}
	if w := loginWith(srv, remote, rightLogin, d); w.Code != http.StatusOK {
		t.Errorf("right login with real device cookie: code %d, want 200", w.Code)
	}
}

func TestSignedInBrowserGetsDeviceCookie(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	const n6, ref = "192.168.65.6:1000", "http://192.168.1.5:6410/"
	s := sessionCookie(loginFrom(srv, n6, rightLogin))
	if s == nil {
		t.Fatal("login: no session cookie")
	}
	secret := testSecret(t, srv)
	s2 := &http.Cookie{Name: authCookieName, Value: signSession(secret, "admin", time.Now().Add(authSessionTTL-5*time.Second))}
	sOld := &http.Cookie{Name: authCookieName, Value: signSession(secret, "admin", time.Now().Add(10*24*time.Hour))}

	var devs []*http.Cookie
	for i, c := range []*http.Cookie{s, s2, sOld} {
		w := apiWith(srv, ref, c)
		d := deviceCookieOf(w)
		if w.Code != http.StatusOK || d == nil {
			t.Fatalf("api with session %d: code %d, device cookie %t, want 200 with one", i, w.Code, d != nil)
		}
		if i == 2 && sessionCookie(w) == nil {
			t.Errorf("old session was not renewed")
		}
		devs = append(devs, d)
	}
	d1 := devs[0]
	for i, d := range devs[1:] {
		if deviceIDPart(d.Value) != deviceIDPart(d1.Value) {
			t.Errorf("session %d minted another id", i+1)
		}
	}
	if !d1.HttpOnly || d1.SameSite != http.SameSiteLaxMode || d1.MaxAge != 34560000 || d1.Secure {
		t.Errorf("minted cookie attrs HttpOnly %t SameSite %v MaxAge %d Secure %t", d1.HttpOnly, d1.SameSite, d1.MaxAge, d1.Secure)
	}
	if w := apiWith(srv, ref, s, d1); w.Code != http.StatusOK || deviceCookieOf(w) != nil {
		t.Errorf("api with valid device cookie: code %d, device cookie %t, want 200 without one", w.Code, deviceCookieOf(w) != nil)
	}
	if w := apiWith(srv, "", s); w.Code != http.StatusOK || deviceCookieOf(w) != nil {
		t.Errorf("api without scheme: code %d, device cookie %t, want 200 without one", w.Code, deviceCookieOf(w) != nil)
	}
	if d := deviceCookieOf(apiWith(srv, "https://my.keenetic.pro/x", s)); d == nil || !d.Secure {
		t.Errorf("api over https: device cookie %v, want a Secure one", d)
	}
	if w := apiWith(srv, ref); w.Code != http.StatusUnauthorized || deviceCookieOf(w) != nil {
		t.Errorf("api without session: code %d, device cookie %t, want 401 without one", w.Code, deviceCookieOf(w) != nil)
	}

	lockAddress(t, srv, n6)
	w := loginWith(srv, n6, rightLogin, d1)
	own := deviceCookieOf(w)
	if w.Code != http.StatusOK || own == nil || deviceIDPart(own.Value) == deviceIDPart(d1.Value) {
		t.Fatalf("login with minted cookie: code %d, device cookie %v, want 200 with an own id", w.Code, own)
	}
	if w := logoutWith(srv, sessionCookie(w)); w.Code != http.StatusOK {
		t.Fatalf("logout: code %d", w.Code)
	}
	s4 := sessionCookie(loginFrom(srv, "192.168.1.46:1000", rightLogin))
	if s4 == nil {
		t.Fatal("login after logout: no session cookie")
	}
	if d := deviceCookieOf(apiWith(srv, ref, s4)); d == nil || deviceIDPart(d.Value) == deviceIDPart(d1.Value) {
		t.Errorf("api in a new epoch: device cookie %v, want one with another id", d)
	}
}

func TestPasswordChangeKeysByDeviceAndRevokesTrust(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	wa := loginWith(srv, nat, rightLogin)
	sa, da := sessionCookie(wa), deviceCookieOf(wa)
	wb := loginWith(srv, nat, rightLogin)
	sb, db := sessionCookie(wb), deviceCookieOf(wb)
	if sa == nil || da == nil || sb == nil || db == nil {
		t.Fatalf("logins: SA %t DA %t SB %t DB %t", sa != nil, da != nil, sb != nil, db != nil)
	}
	oldKey := storedDeviceKey(t, srv)

	for i := range maxLoginFails {
		if w := passwordWithCookies(srv, nat, wrongChange, sa, da); w.Code != http.StatusForbidden {
			t.Fatalf("wrong change %d with DA: code %d, want 403", i+1, w.Code)
		}
	}
	if w := passwordWithCookies(srv, nat, wrongChange, sa, da); w.Code != http.StatusTooManyRequests || errorText(t, w) != "too many attempts, try later" {
		t.Fatalf("6th wrong change with DA: code %d body %s, want 429", w.Code, w.Body.String())
	}
	if w := loginWith(srv, nat, rightLogin, db); w.Code != http.StatusOK {
		t.Errorf("login with DB after DA lock: code %d, want 200", w.Code)
	}
	if w := loginWith(srv, nat, rightLogin); w.Code != http.StatusOK {
		t.Errorf("untrusted login after DA lock: code %d, want 200", w.Code)
	}

	w := passwordWithCookies(srv, nat, changeBody, sb, db)
	devs := deviceCookiesAll(w)
	if w.Code != http.StatusOK || len(sessionCookiesAll(w)) != 1 || len(devs) != 1 {
		t.Fatalf("change with DB: code %d, %d session and %d device cookies, want 200, 1 and 1", w.Code, len(sessionCookiesAll(w)), len(devs))
	}
	dn := devs[0]
	if bytes.Equal(storedDeviceKey(t, srv), oldKey) {
		t.Error("password change kept device_key")
	}
	lockAddress(t, srv, nat)
	if w := loginWith(srv, nat, newLogin, db); w.Code != http.StatusTooManyRequests {
		t.Errorf("login with pre-change device cookie on locked address: code %d, want 429", w.Code)
	}
	if w := loginWith(srv, nat, newLogin, dn); w.Code != http.StatusOK {
		t.Errorf("login with new device cookie: code %d, want 200", w.Code)
	}
}

func TestFullDeviceTableFallsBackToAddress(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	const n8 = "192.168.65.8:1000"
	d := deviceCookieOf(loginWith(srv, n8, rightLogin))
	e := deviceCookieOf(loginWith(srv, n8, rightLogin))
	if d == nil || e == nil {
		t.Fatalf("login: device D %t, E %t", d != nil, e != nil)
	}
	wantAttemptsLeft(t, loginWith(srv, n8, wrongLogin, e), "wrong login with E", maxLoginFails-1)
	for i := 0; len(srv.deviceFails.fails) < maxThrottleKeys; i++ {
		srv.deviceFails.reserve(fmt.Sprintf("filler%d", i))
	}

	if w := loginWith(srv, n8, rightLogin, d); w.Code != http.StatusOK {
		t.Fatalf("right login with D on a full device table: code %d, want 200", w.Code)
	}
	wantAttemptsLeft(t, loginWith(srv, n8, wrongLogin, d), "wrong login with D", maxLoginFails-1)
	wantAttemptsLeft(t, loginWith(srv, n8, wrongLogin), "wrong login without device", maxLoginFails-2)
	wantAttemptsLeft(t, loginWith(srv, n8, wrongLogin, e), "second wrong login with E", maxLoginFails-2)
	if n := len(srv.deviceFails.fails); n != maxThrottleKeys {
		t.Errorf("device table has %d keys, want %d", n, maxThrottleKeys)
	}
}

func cookieNamed(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func historyCookies(srv *Server, host, referer string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = host
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	addCookies(req, cookies)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

const (
	proxyHost     = "192.168.1.105"
	hostSession   = "__Host-ripperr_session"
	hostDevice    = "__Host-ripperr_device"
	legacySession = "ripperr_session"
	legacyDevice  = "ripperr_device"
)

func TestSecureCookiesUseHostPrefix(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})

	w := authPost(srv, "/api/auth/login", proxyHost, "", rightLogin)
	if w.Code != http.StatusOK {
		t.Fatalf("proxy login: code %d", w.Code)
	}
	s := cookieNamed(w, hostSession)
	if s == nil || !s.Secure || s.Path != "/" || s.Domain != "" || !s.HttpOnly || s.SameSite != http.SameSiteLaxMode || s.MaxAge != int(authSessionTTL.Seconds()) {
		t.Errorf("proxy login session cookie = %+v, want %s Secure, Path /, no Domain, HttpOnly, Lax, MaxAge %d", s, hostSession, int(authSessionTTL.Seconds()))
	}
	if d := cookieNamed(w, hostDevice); d == nil || !d.Secure || d.Path != "/" || d.Domain != "" {
		t.Errorf("proxy login device cookie = %+v, want %s Secure, Path /, no Domain", d, hostDevice)
	}
	for _, name := range []string{legacySession, legacyDevice} {
		if c := cookieNamed(w, name); c == nil || c.MaxAge >= 0 {
			t.Errorf("proxy login %s = %+v, want a deletion", name, c)
		}
	}

	w = authPost(srv, "/api/auth/login", "192.168.1.5:6410", "http://192.168.1.5:6410", rightLogin)
	if w.Code != http.StatusOK {
		t.Fatalf("LAN login: code %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if strings.HasPrefix(c.Name, "__Host-") {
			t.Errorf("LAN login set %s, want only legacy names", c.Name)
		}
	}
	for _, name := range []string{legacySession, legacyDevice} {
		if c := cookieNamed(w, name); c == nil || c.MaxAge <= 0 {
			t.Errorf("LAN login %s = %+v, want a live cookie", name, c)
		}
	}
}

func TestLegacyCookiesMigrateOverHTTPS(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	d := cookieNamed(loginWith(srv, nat, rightLogin), legacyDevice)
	if d == nil {
		t.Fatal("LAN login: no device cookie")
	}
	// свежая сессия: продление по возрасту не сработает, куку меняет только переезд на новое имя
	s := &http.Cookie{Name: legacySession, Value: signSession(testSecret(t, srv), "admin", time.Now().Add(authSessionTTL-time.Hour))}

	w := historyCookies(srv, proxyHost, "", s, d)
	if w.Code != http.StatusOK {
		t.Fatalf("proxy request with legacy cookies: code %d", w.Code)
	}
	ns := cookieNamed(w, hostSession)
	if ns == nil || !ns.Secure {
		t.Fatalf("proxy request: %s = %+v, want a Secure one", hostSession, ns)
	}
	if c := cookieNamed(w, legacySession); c == nil || c.MaxAge >= 0 {
		t.Errorf("proxy request: %s = %+v, want a deletion", legacySession, c)
	}
	nd := cookieNamed(w, hostDevice)
	if nd == nil || deviceIDPart(nd.Value) != deviceIDPart(d.Value) {
		t.Errorf("proxy request: %s = %+v, want the same device id %s", hostDevice, nd, deviceIDPart(d.Value))
	}
	if c := cookieNamed(w, legacyDevice); c == nil || c.MaxAge >= 0 {
		t.Errorf("proxy request: %s = %+v, want a deletion", legacyDevice, c)
	}
	if w := historyCookies(srv, proxyHost, "", ns); w.Code != http.StatusOK {
		t.Errorf("proxy request with only %s: code %d, want 200", hostSession, w.Code)
	}

	w = historyCookies(srv, "192.168.1.5:6410", "http://192.168.1.5:6410/", s, d)
	if w.Code != http.StatusOK {
		t.Fatalf("LAN request with legacy cookies: code %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if strings.HasPrefix(c.Name, "__Host-") || c.MaxAge < 0 {
			t.Errorf("LAN request set %s Max-Age %d, want no prefixed cookie and no deletion", c.Name, c.MaxAge)
		}
	}
}

func TestDuplicateCookiesFirstValidWins(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	const lan = "192.168.1.5:6410"
	valid := &http.Cookie{Name: legacySession, Value: signSession(testSecret(t, srv), "admin", time.Now().Add(authSessionTTL-time.Hour))}

	if w := historyCookies(srv, lan, "", &http.Cookie{Name: legacySession, Value: "garbage"}, valid); w.Code != http.StatusOK {
		t.Errorf("garbage then valid %s: code %d, want 200", legacySession, w.Code)
	}
	foreign := &http.Cookie{Name: hostSession, Value: signSession([]byte("not the secret"), "admin", time.Now().Add(time.Hour))}
	if w := historyCookies(srv, lan, "", foreign, valid); w.Code != http.StatusOK {
		t.Errorf("foreign %s plus valid %s: code %d, want 200", hostSession, legacySession, w.Code)
	}

	d := cookieNamed(loginWith(srv, nat, rightLogin), legacyDevice)
	if d == nil {
		t.Fatal("login: no device cookie")
	}
	const remote = "198.51.100.32:1000"
	lockAddress(t, srv, remote)
	forged := &http.Cookie{Name: legacyDevice, Value: signSession([]byte("not the device key"), "fakeid", time.Now().Add(time.Hour))}
	if w := loginWith(srv, remote, rightLogin, forged, d); w.Code != http.StatusOK {
		t.Errorf("forged then valid %s on a locked address: code %d, want 200", legacyDevice, w.Code)
	}
}

func TestLogoutClearsHostCookies(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	s := &http.Cookie{Name: hostSession, Value: signSession(testSecret(t, srv), "admin", time.Now().Add(authSessionTTL-time.Hour))}

	w := csrfRequest(srv, "POST", "/api/auth/logout", proxyHost, "", "1", "", "", s)
	if w.Code != http.StatusOK {
		t.Fatalf("proxy logout: code %d", w.Code)
	}
	if c := cookieNamed(w, hostSession); c == nil || c.MaxAge >= 0 || !c.Secure || c.Path != "/" {
		t.Errorf("proxy logout %s = %+v, want a Secure deletion with Path /", hostSession, c)
	}
	if c := cookieNamed(w, legacySession); c == nil || c.MaxAge >= 0 {
		t.Errorf("proxy logout %s = %+v, want a deletion", legacySession, c)
	}
	for _, name := range []string{hostDevice, legacyDevice} {
		if c := cookieNamed(w, name); c != nil {
			t.Errorf("proxy logout set %s, want device cookies kept", name)
		}
	}
	if w := historyCookies(srv, proxyHost, "", s); w.Code != http.StatusUnauthorized {
		t.Errorf("old %s after logout: code %d, want 401", hostSession, w.Code)
	}
}

func wantLoginLocked(t *testing.T, w *httptest.ResponseRecorder, step string) {
	t.Helper()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("%s: code %d body %s, want 429", step, w.Code, w.Body.String())
	}
	if got := loginRespOf(t, w).Error; got != "too many attempts, try later" {
		t.Errorf("%s: error %q, want the lock text", step, got)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Errorf("%s: no Retry-After header", step)
	}
}

func TestLockedLoginTakesNoHashSlot(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	const remote = "198.51.100.40:1000"
	lockAddress(t, srv, remote)
	fillHashSlots(srv)
	wantLoginLocked(t, loginWith(srv, remote, wrongLogin), "locked address with busy slots")
	drainHashSlots(srv)

	const n9 = "192.168.65.9:1000"
	d := deviceCookieOf(loginWith(srv, n9, rightLogin))
	if d == nil {
		t.Fatal("login: no device cookie")
	}
	for i := range maxLoginFails {
		wantAttemptsLeft(t, loginWith(srv, n9, wrongLogin, d), fmt.Sprintf("device wrong login %d", i+1), maxLoginFails-i-1)
	}
	fillHashSlots(srv)
	wantLoginLocked(t, loginWith(srv, n9, wrongLogin, d), "locked device with busy slots")
	drainHashSlots(srv)
}

func TestSetupConfiguredSkipsSlotAndDB(t *testing.T) {
	store := &countingSettingStore{}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = config.Web{Listen: ":0", Enabled: true}
		return cfg
	}
	setup := func(host string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/auth/setup", strings.NewReader(rightLogin))
		req.Header.Set("X-Ripperr", "1")
		req.RemoteAddr = "192.168.1.10:1234"
		req.Host = host
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		return w
	}
	if w := setup("192.168.1.5:6410"); w.Code != http.StatusOK {
		t.Fatalf("first setup: code %d", w.Code)
	}
	store.getSettingCalls = 0

	if w := setup("192.168.1.5:6410"); w.Code != http.StatusConflict {
		t.Errorf("setup on configured server: code %d, want 409", w.Code)
	}
	if store.getSettingCalls != 0 {
		t.Errorf("GetSetting calls = %d, want 0 (configured state comes from the cache)", store.getSettingCalls)
	}
	fillHashSlots(srv)
	if w := setup("192.168.1.5:6410"); w.Code != http.StatusConflict {
		t.Errorf("setup on configured server with busy slots: code %d, want 409", w.Code)
	}
	if w := setup(proxyHost); w.Code != http.StatusForbidden {
		t.Errorf("setup through the proxy with busy slots: code %d, want 403", w.Code)
	}
	drainHashSlots(srv)
}

func TestCookieCandidatesCapped(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	const lan = "192.168.1.5:6410"
	valid := &http.Cookie{Name: legacySession, Value: signSession(testSecret(t, srv), "admin", time.Now().Add(authSessionTTL-time.Hour))}
	garbage := func(n int) []*http.Cookie {
		out := make([]*http.Cookie, 0, n+1)
		for i := range n {
			out = append(out, &http.Cookie{Name: legacySession, Value: fmt.Sprintf("garbage%d", i)})
		}
		return append(out, valid)
	}
	if w := historyCookies(srv, lan, "", garbage(16)...); w.Code != http.StatusUnauthorized {
		t.Errorf("valid %s after 16 garbage ones: code %d, want 401", legacySession, w.Code)
	}
	if w := historyCookies(srv, lan, "", garbage(15)...); w.Code != http.StatusOK {
		t.Errorf("valid %s as the 16th: code %d, want 200", legacySession, w.Code)
	}
}
