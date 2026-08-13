package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"trackerr/internal/config"
)

func TestAuthSetupThenLoginFlow(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = &config.Web{Listen: ":0", Enabled: true, LanBypass: true, TrustedProxy: false}
		return cfg
	}

	// Setup: create account
	body := []byte(`{"username":"testuser","password":"testpass123"}`)
	req := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("setup failed: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify setup response
	var setupResp map[string]bool
	json.Unmarshal(w.Body.Bytes(), &setupResp)
	if !setupResp["ok"] {
		t.Error("setup response ok=false")
	}

	// Extract cookie from setup
	setupCookie := w.Header().Get("Set-Cookie")
	if setupCookie == "" {
		t.Error("setup did not set cookie")
	}

	// Setup again should fail with 409
	req = httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate setup: expected 409, got %d", w.Code)
	}

	// Login with correct password
	loginBody := []byte(`{"username":"testuser","password":"testpass123"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("login failed: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Extract login cookie
	loginCookie := w.Header().Get("Set-Cookie")
	if loginCookie == "" {
		t.Error("login did not set cookie")
	}

	// Login with wrong password
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
	srv := newTestServer(store)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = &config.Web{Listen: ":0", Enabled: true, LanBypass: false, TrustedProxy: false}
		return cfg
	}

	// Setup account
	setupBody := []byte(`{"username":"admin","password":"adminpass123"}`)
	req := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(setupBody))
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup failed: %d", w.Code)
	}

	// Request /api/history from non-local address without cookie should return 401
	req = httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unauth api call: expected 401, got %d", w.Code)
	}

	// Login to get valid cookie
	loginBody := []byte(`{"username":"admin","password":"adminpass123"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d", w.Code)
	}

	// Extract cookie value from Set-Cookie header
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

	// Request /api/history with valid cookie should succeed (not 401)
	req = httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("auth api call with cookie should not be 401, got %d", w.Code)
	}
}

func TestAuthLanBypass(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = &config.Web{Listen: ":0", Enabled: true, LanBypass: true, TrustedProxy: false}
		return cfg
	}

	// Setup account
	setupBody := []byte(`{"username":"admin","password":"adminpass123"}`)
	req := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(setupBody))
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup failed: %d", w.Code)
	}

	// Request /api/history from local address without cookie should succeed (LAN bypass)
	req = httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "192.168.1.10:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("local api call should not be 401 with LAN bypass, got %d", w.Code)
	}
}

func TestAuthOpenUntilConfigured(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = &config.Web{Listen: ":0", Enabled: true, LanBypass: true, TrustedProxy: false}
		return cfg
	}

	// Request /api/history from non-local address without auth setup should succeed (no account yet)
	req := httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("api call without configured auth should not be 401, got %d", w.Code)
	}

	// GET /api/auth/state should show not configured
	req = httptest.NewRequest("GET", "/api/auth/state", nil)
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("auth/state failed: %d", w.Code)
	}

	var state map[string]any
	json.Unmarshal(w.Body.Bytes(), &state)
	if state["configured"].(bool) {
		t.Error("auth state should show configured=false before setup")
	}
}

func TestLoginPageServed(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = &config.Web{Listen: ":0", Enabled: true, LanBypass: true, TrustedProxy: false}
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
