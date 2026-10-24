package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/notify"
	"ripperr/internal/runner"
	"ripperr/internal/settings"
	"ripperr/internal/system/logger"
	"ripperr/internal/webpush"
)

const listenWait = 2 * time.Second

func newListenServer(t *testing.T, store *lockedSettingStore, reload func()) *Server {
	t.Helper()
	effective := func() config.App {
		cfg := testEffective()
		cfg.Web.Listen = "127.0.0.1:0"
		if err := settings.ApplyOverrides(context.Background(), &cfg, store); err != nil {
			t.Errorf("apply overrides: %v", err)
		}
		return cfg
	}
	return withTestSession(New(store, reload, effective, "127.0.0.1:0", runner.NewState(), nil))
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func holdPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func localAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

func startListenServer(t *testing.T, srv *Server) <-chan error {
	t.Helper()
	errc := make(chan error, 1)
	go func() { errc <- srv.Start() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return errc
}

func waitServing(t *testing.T, srv *Server) int {
	t.Helper()
	deadline := time.Now().Add(listenWait)
	for time.Now().Before(deadline) {
		if a, ok := srv.Addr().(*net.TCPAddr); ok {
			waitDial(t, localAddr(a.Port))
			return a.Port
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server has no listener after %v", listenWait)
	return 0
}

func dialOK(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func waitDial(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(listenWait)
	for time.Now().Before(deadline) {
		if dialOK(addr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("dial %s: still refused after %v", addr, listenWait)
}

func waitRefused(t *testing.T, addr string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if !dialOK(addr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("dial %s: still accepted after %v", addr, limit)
}

var listenClient = &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}

func doListen(t *testing.T, method string, port int, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+localAddr(port)+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Ripperr", "1")
	resp, err := listenClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s via %d: %v", method, path, port, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

type webResp struct {
	AllowedHosts []string `json:"allowed_hosts"`
	Port         int      `json:"port"`
	Moved        bool     `json:"moved"`
}

func decodeWeb(t *testing.T, body string) webResp {
	t.Helper()
	var out webResp
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return out
}

func savedWebPort(t *testing.T, store *lockedSettingStore) int {
	t.Helper()
	raw, ok, _ := store.GetSetting(context.Background(), "web")
	if !ok {
		return 0
	}
	var v struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v.Port
}

func hasFallbackWarn(port int) bool {
	needle := "cannot listen on " + localAddr(port)
	for _, e := range logger.Snapshot(0) {
		if e.Level == "WARN" && strings.Contains(e.Message, needle) {
			return true
		}
	}
	return false
}

func TestStartListensOnSavedPort(t *testing.T) {
	port := freePort(t)
	store := &lockedSettingStore{}
	store.settings = map[string]json.RawMessage{"web": json.RawMessage(fmt.Sprintf(`{"port":%d}`, port))}
	srv := newListenServer(t, store, func() {})
	startListenServer(t, srv)
	waitDial(t, localAddr(port))
	if code, _ := doListen(t, "GET", port, "/healthz", ""); code != http.StatusOK {
		t.Fatalf("healthz via saved port %d: code %d, want 200", port, code)
	}
}

func TestPutWebBindFailureKeepsOldPort(t *testing.T) {
	store := &lockedSettingStore{}
	srv := newListenServer(t, store, func() {})
	startListenServer(t, srv)
	a := waitServing(t, srv)
	b := holdPort(t)

	code, body := doListen(t, "PUT", a, "/api/web", fmt.Sprintf(`{"port":%d}`, b))
	if code != http.StatusConflict || !strings.Contains(body, "not available") {
		t.Fatalf("PUT busy port: code %d body %q, want 409 with 'not available'", code, body)
	}
	if code, _ = doListen(t, "GET", a, "/healthz", ""); code != http.StatusOK {
		t.Fatalf("healthz via old port after 409: code %d", code)
	}
	code, body = doListen(t, "GET", a, "/api/web", "")
	if code != http.StatusOK || decodeWeb(t, body).Port != a {
		t.Fatalf("GET /api/web: code %d body %q, want port %d", code, body, a)
	}
	if p := savedWebPort(t, store); p != 0 {
		t.Fatalf("saved port %d after failed bind, want none", p)
	}
}

func TestPutWebMovesListener(t *testing.T) {
	store := &lockedSettingStore{}
	srv := newListenServer(t, store, func() {})
	startListenServer(t, srv)
	a := waitServing(t, srv)
	p := freePort(t)

	code, body := doListen(t, "PUT", a, "/api/web", fmt.Sprintf(`{"port":%d}`, p))
	if code != http.StatusOK {
		t.Fatalf("PUT port: code %d body %q, want 200", code, body)
	}
	if got := decodeWeb(t, body); got.Port != p || !got.Moved {
		t.Fatalf("PUT port: body %q, want port %d moved true", body, p)
	}
	if code, _ = doListen(t, "GET", p, "/healthz", ""); code != http.StatusOK {
		t.Fatalf("healthz via new port: code %d", code)
	}
	waitRefused(t, localAddr(a), time.Second)
	code, body = doListen(t, "GET", p, "/api/web", "")
	if code != http.StatusOK || decodeWeb(t, body).Port != p {
		t.Fatalf("GET /api/web via new port: code %d body %q, want port %d", code, body, p)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	// пока P занят, второй сервер ушел бы на запасной адрес и тест бы ничего не проверил
	waitRefused(t, localAddr(p), listenWait)
	second := newListenServer(t, store, func() {})
	startListenServer(t, second)
	if got := waitServing(t, second); got != p {
		t.Fatalf("second server serves on %d, want saved %d", got, p)
	}
	if hasFallbackWarn(p) {
		t.Fatalf("unexpected fallback WARN for port %d", p)
	}
}

func TestPutWebAllowedHostsImmediate(t *testing.T) {
	store := &lockedSettingStore{}
	reloads := 0
	srv := newListenServer(t, store, func() { reloads++ })
	logout := func() int {
		req := httptest.NewRequest("POST", "/api/auth/logout", nil)
		req.RemoteAddr = "192.168.1.10:1234"
		req.Host = "192.168.1.200:6410"
		req.Header.Set("Origin", "https://my.keenetic.pro")
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		return w.Code
	}
	if code := logout(); code != http.StatusForbidden {
		t.Fatalf("logout before allowed_hosts: code %d, want 403", code)
	}
	req := httptest.NewRequest("PUT", "/api/web", bytes.NewReader([]byte(`{"allowed_hosts":["my.keenetic.pro"]}`)))
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = "192.168.1.200:6410"
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT allowed_hosts: code %d body %q, want 200", w.Code, w.Body.String())
	}
	if got := decodeWeb(t, w.Body.String()); got.Moved {
		t.Fatalf("PUT allowed_hosts: body %q, want moved false", w.Body.String())
	}
	if code := logout(); code != http.StatusOK {
		t.Fatalf("logout after allowed_hosts: code %d, want 200", code)
	}
	if reloads != 0 {
		t.Fatalf("reload called %d times, want 0", reloads)
	}
}

func TestPutWebValidation(t *testing.T) {
	for _, body := range []string{`{"port":0}`, `{"port":70000}`, `{"allowed_hosts":["https://x"]}`} {
		t.Run(body, func(t *testing.T) {
			store := &lockedSettingStore{}
			srv := newListenServer(t, store, func() {})
			req := httptest.NewRequest("PUT", "/api/web", strings.NewReader(body))
			w := httptest.NewRecorder()
			srv.srv.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("code %d body %q, want 400", w.Code, w.Body.String())
			}
			if _, ok, _ := store.GetSetting(context.Background(), "web"); ok {
				t.Fatal("web setting saved after a rejected patch")
			}
		})
	}
}

func TestStartFallsBackWhenSavedPortBusy(t *testing.T) {
	p := holdPort(t)
	store := &lockedSettingStore{}
	store.settings = map[string]json.RawMessage{"web": json.RawMessage(fmt.Sprintf(`{"port":%d}`, p))}
	srv := newListenServer(t, store, func() {})
	startListenServer(t, srv)
	got := waitServing(t, srv)
	if got == p {
		t.Fatalf("serves on busy port %d", p)
	}
	if !hasFallbackWarn(p) {
		t.Fatalf("no WARN 'cannot listen on %s' in the log", localAddr(p))
	}
	code, body := doListen(t, "GET", got, "/api/web", "")
	if code != http.StatusOK || decodeWeb(t, body).Port != got {
		t.Fatalf("GET /api/web: code %d body %q, want fallback port %d", code, body, got)
	}
}

func TestShutdownClosesAllListeners(t *testing.T) {
	store := &lockedSettingStore{}
	srv := newListenServer(t, store, func() {})
	errc := startListenServer(t, srv)
	a := waitServing(t, srv)
	p := freePort(t)
	if code, body := doListen(t, "PUT", a, "/api/web", fmt.Sprintf(`{"port":%d}`, p)); code != http.StatusOK {
		t.Fatalf("PUT port: code %d body %q", code, body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("Start returned %v, want http.ErrServerClosed", err)
		}
	case <-time.After(listenWait):
		t.Fatal("Start still blocked after Shutdown")
	}
	if dialOK(localAddr(p)) {
		t.Fatalf("port %d still accepts after Shutdown", p)
	}
}

func newLockedListenServer(t *testing.T, store *lockedSettingStore) *Server {
	t.Helper()
	effective := func() config.App {
		cfg := testEffective()
		cfg.Web.Listen = "127.0.0.1:0"
		cfg.Web.PortLocked = true
		if err := settings.ApplyOverrides(context.Background(), &cfg, store); err != nil {
			t.Errorf("apply overrides: %v", err)
		}
		return cfg
	}
	return withTestSession(New(store, func() {}, effective, "127.0.0.1:0", runner.NewState(), nil))
}

func TestGetWebReportsPortLocked(t *testing.T) {
	for _, locked := range []bool{false, true} {
		store := &lockedSettingStore{}
		srv := newListenServer(t, store, func() {})
		if locked {
			srv = newLockedListenServer(t, store)
		}
		startListenServer(t, srv)
		port := waitServing(t, srv)
		code, body := doListen(t, "GET", port, "/api/web", "")
		var got struct {
			PortLocked bool `json:"port_locked"`
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil || code != http.StatusOK {
			t.Fatalf("GET /api/web: code %d body %q err %v", code, body, err)
		}
		if got.PortLocked != locked || !strings.Contains(body, `"port_locked"`) {
			t.Fatalf("locked=%v: body %q", locked, body)
		}
	}
}

func TestPutWebLockedRejectsOtherPort(t *testing.T) {
	store := &lockedSettingStore{}
	srv := newLockedListenServer(t, store)
	startListenServer(t, srv)
	a := waitServing(t, srv)
	p := freePort(t)

	code, body := doListen(t, "PUT", a, "/api/web", fmt.Sprintf(`{"port":%d}`, p))
	if code != http.StatusBadRequest || !strings.Contains(body, "web port is fixed in Docker: change the host side of -p instead") {
		t.Fatalf("PUT other port: code %d body %q, want 400 with the Docker message", code, body)
	}
	if code, _ = doListen(t, "GET", a, "/healthz", ""); code != http.StatusOK {
		t.Fatalf("healthz via old port: code %d", code)
	}
	waitRefused(t, localAddr(p), time.Second)
	if _, ok, _ := store.GetSetting(context.Background(), "web"); ok {
		t.Fatal("web setting saved after a rejected port")
	}
}

func TestPutWebLockedKeepsAllowedHosts(t *testing.T) {
	store := &lockedSettingStore{}
	srv := newLockedListenServer(t, store)
	startListenServer(t, srv)
	a := waitServing(t, srv)

	for _, body := range []string{`{"allowed_hosts":["my.keenetic.pro"]}`, fmt.Sprintf(`{"port":%d,"allowed_hosts":["my.keenetic.pro"]}`, a)} {
		code, resp := doListen(t, "PUT", a, "/api/web", body)
		if code != http.StatusOK || decodeWeb(t, resp).Moved {
			t.Fatalf("PUT %s: code %d body %q, want 200 moved false", body, code, resp)
		}
	}
	code, resp := doListen(t, "GET", a, "/api/web", "")
	if got := decodeWeb(t, resp); code != http.StatusOK || got.Port != a || !slices.Equal(got.AllowedHosts, []string{"my.keenetic.pro"}) {
		t.Fatalf("GET after save: code %d body %q", code, resp)
	}
}

func putWebLocal(srv *Server, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("PUT", "/api/web", strings.NewReader(body))
	req.RemoteAddr = "192.168.1.10:1234"
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func getWebAppURL(t *testing.T, srv *Server) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/web", nil)
	req.RemoteAddr = "192.168.1.10:1234"
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	var got struct {
		AppURL *string `json:"app_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusOK || got.AppURL == nil {
		t.Fatalf("GET /api/web: code %d body %q err %v, want app_url field", w.Code, w.Body.String(), err)
	}
	return *got.AppURL
}

func TestAppURLRoundTripAndReload(t *testing.T) {
	for _, locked := range []bool{false, true} {
		t.Run(fmt.Sprintf("locked=%v", locked), func(t *testing.T) {
			store := &lockedSettingStore{}
			reloads := 0
			srv := newListenServer(t, store, func() { reloads++ })
			if locked {
				srv = newLockedListenServer(t, store)
				srv.reload = func() { reloads++ }
			}
			if got := getWebAppURL(t, srv); got != "" {
				t.Fatalf("initial app_url = %q", got)
			}
			if w := putWebLocal(srv, `{"app_url":"https://rip.example.com/app/"}`); w.Code != http.StatusOK {
				t.Fatalf("PUT: code %d body %q", w.Code, w.Body.String())
			}
			if got := getWebAppURL(t, srv); got != "https://rip.example.com/app" {
				t.Fatalf("app_url = %q, want trimmed slash", got)
			}
			if reloads != 1 {
				t.Fatalf("reloads = %d after changing app_url, want 1 (notifier is rebuilt on reload)", reloads)
			}
			if w := putWebLocal(srv, `{"app_url":"https://rip.example.com/app"}`); w.Code != http.StatusOK || reloads != 1 {
				t.Fatalf("same value: code %d reloads %d, want no extra reload", w.Code, reloads)
			}
			if w := putWebLocal(srv, `{"allowed_hosts":["x.lan"]}`); w.Code != http.StatusOK || reloads != 1 || getWebAppURL(t, srv) != "https://rip.example.com/app" {
				t.Fatalf("patch without app_url must keep it: code %d reloads %d", w.Code, reloads)
			}
			if w := putWebLocal(srv, `{"app_url":""}`); w.Code != http.StatusOK {
				t.Fatalf("clear: code %d", w.Code)
			}
			if got := getWebAppURL(t, srv); got != "" || reloads != 2 {
				t.Fatalf("after clear app_url = %q reloads %d", got, reloads)
			}
		})
	}
}

func TestPutWebAppURLInvalid(t *testing.T) {
	store := &lockedSettingStore{}
	srv := newListenServer(t, store, func() {})
	w := putWebLocal(srv, `{"app_url":"ftp://x"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "app_url") {
		t.Fatalf("code %d body %q, want 400 naming app_url", w.Code, w.Body.String())
	}
	if _, ok, _ := store.GetSetting(context.Background(), "web"); ok {
		t.Fatal("web setting saved after a rejected app_url")
	}
}

func TestGetWebAppURLFromEnvDefault(t *testing.T) {
	store := &lockedSettingStore{}
	effective := func() config.App {
		cfg := testEffective()
		cfg.Web.AppURL = "https://env.example.com"
		_ = settings.ApplyOverrides(context.Background(), &cfg, store)
		return cfg
	}
	srv := withTestSession(New(store, func() {}, effective, "127.0.0.1:0", runner.NewState(), nil))
	if got := getWebAppURL(t, srv); got != "https://env.example.com" {
		t.Fatalf("app_url = %q, want env default", got)
	}
	if w := putWebLocal(srv, `{"app_url":"https://db.example.com"}`); w.Code != http.StatusOK {
		t.Fatalf("PUT: %d", w.Code)
	}
	if got := getWebAppURL(t, srv); got != "https://db.example.com" {
		t.Fatalf("app_url = %q, want stored value to win", got)
	}
}

func TestPostIntegrationsTestNotifyCarriesAppLink(t *testing.T) {
	origSend := sendNotification
	defer func() { sendNotification = origSend }()
	var sent notify.Event
	sendNotification = func(_ context.Context, _ notify.Config, _ string, _ *webpush.Store, ev notify.Event) error {
		sent = ev
		return nil
	}
	for _, appURL := range []string{"https://rip.example.com", ""} {
		effective := func() config.App {
			cfg := testEffective()
			cfg.Notify.WebhookURL = "http://hook.invalid"
			cfg.Web.AppURL = appURL
			return cfg
		}
		srv := New(&fakeStore{}, func() {}, effective, ":0", runner.NewState(), nil)
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", strings.NewReader(`{"kind":"notify"}`))
		srv.handleTestIntegration(httptest.NewRecorder(), req)
		want := ""
		if appURL != "" {
			want = appURL + "/#dashboard"
		}
		if sent.AppLink != want {
			t.Errorf("app url %q: AppLink = %q, want %q", appURL, sent.AppLink, want)
		}
	}
}
