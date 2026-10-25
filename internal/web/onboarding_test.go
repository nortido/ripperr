package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ripperr/internal/model"
)

func onboardingGet(t *testing.T, srv *Server) (int, map[string]bool, string) {
	t.Helper()
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/onboarding", nil))
	var got map[string]bool
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode %q: %v", w.Body.String(), err)
		}
	}
	return w.Code, got, w.Header().Get("Cache-Control")
}

func onboardingPut(srv *Server, body string) int {
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/onboarding", strings.NewReader(body)))
	return w.Code
}

func TestOnboardingFresh(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		return nil, errors.New("no client")
	}
	srv := newTestServer(&fakeStore{settings: map[string]json.RawMessage{"trackers_added": json.RawMessage(`[]`)}})

	code, got, cc := onboardingGet(t, srv)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	want := []string{"checklist_hidden", "tour_done", "replay", "client_ok", "tracker_ok", "watch_ok"}
	if len(got) != len(want) {
		t.Errorf("fields = %v", got)
	}
	for _, k := range want {
		if v, ok := got[k]; !ok || v {
			t.Errorf("%s = %v (present %v), want false", k, v, ok)
		}
	}
}

func TestOnboardingConfigured(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		return &fakeQbitClient{cats: []string{"tv"}}, nil
	}
	srv := newTestServer(&fakeStore{settings: map[string]json.RawMessage{
		"trackers_added": json.RawMessage(`["rutracker"]`),
		"schedule":       json.RawMessage(`{"watch_mode":"categories"}`),
	}})

	code, got, _ := onboardingGet(t, srv)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	for _, k := range []string{"client_ok", "tracker_ok", "watch_ok"} {
		if !got[k] {
			t.Errorf("%s = false, want true", k)
		}
	}
	if got["checklist_hidden"] || got["tour_done"] {
		t.Errorf("flags set: %v", got)
	}
}

func TestOnboardingHiddenSkipsProbe(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()
	calls := 0
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		calls++
		return &fakeQbitClient{}, nil
	}
	srv := newTestServer(&fakeStore{settings: map[string]json.RawMessage{
		"onboarding":     json.RawMessage(`{"checklist_hidden":true}`),
		"trackers_added": json.RawMessage(`["rutracker"]`),
		"schedule":       json.RawMessage(`{"watch_mode":"categories"}`),
	}})

	code, got, _ := onboardingGet(t, srv)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if calls != 0 {
		t.Errorf("client factory calls = %d, want 0", calls)
	}
	if !got["checklist_hidden"] || got["client_ok"] || got["tracker_ok"] || got["watch_ok"] {
		t.Errorf("got %v", got)
	}
}

func TestPutOnboarding(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		return nil, errors.New("no client")
	}
	store := &fakeStore{settings: map[string]json.RawMessage{}}
	srv := newTestServer(store)

	if code := onboardingPut(srv, `{"tour_done":true}`); code != http.StatusNoContent {
		t.Fatalf("PUT status %d, want 204", code)
	}
	if _, got, _ := onboardingGet(t, srv); !got["tour_done"] {
		t.Errorf("tour_done not stored: %v", got)
	}
	saved := string(store.settings["onboarding"])

	for _, body := range []string{`{"bogus":1}`, `{}`, `{"tour_done":true} {}`, `{"tour_done":"yes"}`} {
		if code := onboardingPut(srv, body); code != http.StatusBadRequest {
			t.Errorf("PUT %s status %d, want 400", body, code)
		}
		if string(store.settings["onboarding"]) != saved {
			t.Errorf("PUT %s changed the doc: %s", body, store.settings["onboarding"])
		}
	}
}

func TestPutOnboardingReplay(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		return nil, errors.New("no client")
	}
	store := &fakeStore{settings: map[string]json.RawMessage{}}
	srv := newTestServer(store)

	if code := onboardingPut(srv, `{"checklist_hidden":false,"tour_done":false,"replay":true}`); code != http.StatusNoContent {
		t.Fatalf("PUT status %d, want 204", code)
	}
	if _, got, _ := onboardingGet(t, srv); !got["replay"] {
		t.Errorf("replay not stored: %v", got)
	}
	if code := onboardingPut(srv, `{"checklist_hidden":true,"replay":false}`); code != http.StatusNoContent {
		t.Fatalf("PUT status %d, want 204", code)
	}
	if _, got, _ := onboardingGet(t, srv); got["replay"] || !got["checklist_hidden"] {
		t.Errorf("replay not cleared on dismiss: %v", got)
	}
}

func TestPutOnboardingNeedsCSRFHeader(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{}}
	srv := newBareServer(store)
	srv.authCache.set(&authDoc{Username: "admin", Secret: base64.RawURLEncoding.EncodeToString(testSessionSecret)})
	req := httptest.NewRequest(http.MethodPut, "/api/onboarding", strings.NewReader(`{"tour_done":true}`))
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: signSession(testSessionSecret, "admin", time.Now().Add(authSessionTTL))})
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code/100 == 2 {
		t.Errorf("status %d, want non-2xx", w.Code)
	}
	if _, ok := store.settings["onboarding"]; ok {
		t.Error("doc saved without X-Ripperr")
	}
}

func TestOnboardingProbeCached(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()
	var calls atomic.Int32
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		calls.Add(1)
		return &fakeQbitClient{}, nil
	}
	srv := newTestServer(&fakeStore{settings: map[string]json.RawMessage{}})

	for range 2 {
		if _, got, _ := onboardingGet(t, srv); !got["client_ok"] {
			t.Fatalf("client_ok = false, want true")
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("client factory calls = %d, want 1", n)
	}
}

type blockingQbitClient struct {
	fakeQbitClient
	release chan struct{}
}

func (b *blockingQbitClient) GetCategories(context.Context) ([]string, error) {
	<-b.release
	return nil, nil
}

func TestOnboardingProbeBounded(t *testing.T) {
	orig, origTimeout := managerClientFactory, clientProbeTimeout
	release := make(chan struct{})
	defer func() { managerClientFactory, clientProbeTimeout = orig, origTimeout }()
	defer close(release)
	clientProbeTimeout = 100 * time.Millisecond
	var calls atomic.Int32
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		calls.Add(1)
		return &blockingQbitClient{release: release}, nil
	}
	srv := newTestServer(&fakeStore{settings: map[string]json.RawMessage{}})

	start := time.Now()
	for range 2 {
		code, got, _ := onboardingGet(t, srv)
		if code != http.StatusOK || got["client_ok"] {
			t.Fatalf("status %d client_ok %v, want 200 false", code, got["client_ok"])
		}
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("two GETs took %v, want about 200ms", d)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("client factory calls = %d, want 1 (one probe in flight)", n)
	}
}

func dialError() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
}

func TestOnboardingProbeFailureRetriesSoon(t *testing.T) {
	orig, origTTL := managerClientFactory, clientProbeFailTTL
	defer func() { managerClientFactory, clientProbeFailTTL = orig, origTTL }()
	clientProbeFailTTL = 50 * time.Millisecond
	var calls atomic.Int32
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		if calls.Add(1) == 1 {
			return nil, dialError()
		}
		return &fakeQbitClient{}, nil
	}
	srv := newTestServer(&fakeStore{settings: map[string]json.RawMessage{}})

	if _, got, _ := onboardingGet(t, srv); got["client_ok"] {
		t.Fatal("client_ok = true on dial error")
	}
	time.Sleep(100 * time.Millisecond)
	if _, got, _ := onboardingGet(t, srv); !got["client_ok"] {
		t.Fatal("client_ok = false after retry")
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("client factory calls = %d, want 2", n)
	}
}

func TestOnboardingProbeAuthFailureCachedLong(t *testing.T) {
	orig, origTTL := managerClientFactory, clientProbeFailTTL
	defer func() { managerClientFactory, clientProbeFailTTL = orig, origTTL }()
	clientProbeFailTTL = 50 * time.Millisecond
	var calls atomic.Int32
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		calls.Add(1)
		return nil, errors.New("login failed")
	}
	srv := newTestServer(&fakeStore{settings: map[string]json.RawMessage{}})

	for range 2 {
		if _, got, _ := onboardingGet(t, srv); got["client_ok"] {
			t.Fatal("client_ok = true on login failure")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("client factory calls = %d, want 1 (login failure keeps the long TTL)", n)
	}
}

func TestOnboardingProbeSuccessCachedLonger(t *testing.T) {
	orig, origTTL := managerClientFactory, clientProbeFailTTL
	defer func() { managerClientFactory, clientProbeFailTTL = orig, origTTL }()
	clientProbeFailTTL = 50 * time.Millisecond
	var calls atomic.Int32
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		calls.Add(1)
		return &fakeQbitClient{}, nil
	}
	srv := newTestServer(&fakeStore{settings: map[string]json.RawMessage{}})

	for range 2 {
		if _, got, _ := onboardingGet(t, srv); !got["client_ok"] {
			t.Fatal("client_ok = false")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("client factory calls = %d, want 1", n)
	}
}

func TestProbeConnectionFailure(t *testing.T) {
	timeout := &url.Error{Op: "Post", URL: "http://x", Err: context.DeadlineExceeded}
	cases := map[string]bool{
		"dial":         true,
		"wrapped dial": true,
		"dns":          true,
		"url timeout":  true,
		"deadline":     true,
		"login":        false,
		"nil":          false,
	}
	errs := map[string]error{
		"dial":         dialError(),
		"wrapped dial": fmt.Errorf("login: %w", &url.Error{Op: "Post", URL: "http://x", Err: dialError()}),
		"dns":          &net.DNSError{Err: "no such host", Name: "x"},
		"url timeout":  timeout,
		"deadline":     context.DeadlineExceeded,
		"login":        errors.New("login failed"),
	}
	for name, want := range cases {
		if got := connectionFailure(errs[name]); got != want {
			t.Errorf("%s: connectionFailure = %v, want %v", name, got, want)
		}
	}
}
