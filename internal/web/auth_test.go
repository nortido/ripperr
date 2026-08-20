package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"trackerr/internal/config"
	"trackerr/internal/runner"
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
	srv := newTestServer(store)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = config.Web{Listen: ":0", Enabled: true, LanBypass: true, TrustedProxy: false}
		return cfg
	}

	// Настройка: создаём учётку
	body := []byte(`{"username":"testuser","password":"testpass123"}`)
	req := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("setup failed: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Проверяем ответ настройки
	var setupResp map[string]bool
	_ = json.Unmarshal(w.Body.Bytes(), &setupResp)
	if !setupResp["ok"] {
		t.Error("setup response ok=false")
	}

	// Достаём куку из настройки
	setupCookie := w.Header().Get("Set-Cookie")
	if setupCookie == "" {
		t.Error("setup did not set cookie")
	}

	// Повторная настройка должна упасть с 409
	req = httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate setup: expected 409, got %d", w.Code)
	}

	// Вход с верным паролем
	loginBody := []byte(`{"username":"testuser","password":"testpass123"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("login failed: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Достаём куку входа
	loginCookie := w.Header().Get("Set-Cookie")
	if loginCookie == "" {
		t.Error("login did not set cookie")
	}

	// Вход с неверным паролем
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
		cfg.Web = config.Web{Listen: ":0", Enabled: true, LanBypass: false, TrustedProxy: false}
		return cfg
	}

	// Настраиваем учётку
	setupBody := []byte(`{"username":"admin","password":"adminpass123"}`)
	req := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(setupBody))
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup failed: %d", w.Code)
	}

	// Запрос /api/history с нелокального адреса без куки должен вернуть 401
	req = httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("unauth api call: expected 401, got %d", w.Code)
	}

	// Входим, чтобы получить валидную куку
	loginBody := []byte(`{"username":"admin","password":"adminpass123"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d", w.Code)
	}

	// Достаём значение куки из заголовка Set-Cookie
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

	// Запрос /api/history с валидной кукой должен пройти (не 401)
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
		cfg.Web = config.Web{Listen: ":0", Enabled: true, LanBypass: true, TrustedProxy: false}
		return cfg
	}

	// Настраиваем учётку
	setupBody := []byte(`{"username":"admin","password":"adminpass123"}`)
	req := httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(setupBody))
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup failed: %d", w.Code)
	}

	// Запрос /api/history с локального адреса без куки должен пройти (обход для LAN)
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
		cfg.Web = config.Web{Listen: ":0", Enabled: true, LanBypass: true, TrustedProxy: false}
		return cfg
	}

	// Запрос /api/history с нелокального адреса без настроенной авторизации должен пройти (учётки ещё нет)
	req := httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("api call without configured auth should not be 401, got %d", w.Code)
	}

	// GET /api/auth/state должен показать, что не настроено
	req = httptest.NewRequest("GET", "/api/auth/state", nil)
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("auth/state failed: %d", w.Code)
	}

	var state map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	if state["configured"].(bool) {
		t.Error("auth state should show configured=false before setup")
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
		if w.Code == http.StatusUnauthorized {
			t.Fatalf("request %d: unexpected 401", i)
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
		cfg.Web = config.Web{Listen: ":0", Enabled: true, LanBypass: false, TrustedProxy: false}
		return cfg
	}

	// прогреваем кеш состоянием "не настроено"
	req := httptest.NewRequest("GET", "/api/auth/state", nil)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if store.getSettingCalls != 1 {
		t.Fatalf("GetSetting calls after warmup = %d, want 1", store.getSettingCalls)
	}

	setupBody := []byte(`{"username":"admin","password":"newpass123"}`)
	req = httptest.NewRequest("POST", "/api/auth/setup", bytes.NewReader(setupBody))
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("setup failed: %d, body: %s", w.Code, w.Body.String())
	}
	if store.getSettingCalls != 1 {
		t.Fatalf("GetSetting calls after setup = %d, want 1 (setup itself must reuse the cache)", store.getSettingCalls)
	}

	// следующий защищённый запрос должен увидеть новый документ без ещё одного GetSetting
	req = httptest.NewRequest("GET", "/api/history", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth request after setup: expected 401, got %d", w.Code)
	}
	if store.getSettingCalls != 1 {
		t.Fatalf("GetSetting calls after protected request = %d, want 1 (cache went stale)", store.getSettingCalls)
	}

	// неверный пароль должен быть отвергнут
	wrongBody := []byte(`{"username":"admin","password":"wrongpass"}`)
	req = httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(wrongBody))
	req.RemoteAddr = "203.0.113.5:1234"
	w = httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("login with wrong password: expected 401, got %d", w.Code)
	}

	// только что заданные учётные данные должны работать
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

	if store.getSettingCalls != 1 {
		t.Fatalf("GetSetting calls at end = %d, want 1 (cache must serve every read)", store.getSettingCalls)
	}
}

func TestLoginPageServed(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)
	srv.effective = func() config.App {
		cfg := testEffective()
		cfg.Web = config.Web{Listen: ":0", Enabled: true, LanBypass: true, TrustedProxy: false}
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
