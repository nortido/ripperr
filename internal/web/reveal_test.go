package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/runner"
	"ripperr/internal/settings"
)

var revealPlainSeeds = map[string]string{
	"qbit.password":                    "SEC-qp",
	"transmission.password":            "SEC-tp",
	"deluge.password":                  "SEC-dp",
	"downloadstation.password":         "SEC-sp",
	"notifications.telegram.bot_token": "SEC-tg",
	"notifications.email.password":     "SEC-ep",
	"notifications.pushover.app_token": "SEC-pa",
	"notifications.pushover.user_key":  "SEC-pu",
	"metrics.token":                    "SEC-mt",
}

func postReveal(srv *Server, url, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("X-Ripperr", "1")
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func revealValue(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp) != 1 {
		t.Errorf("response = %s, want only value", w.Body.String())
	}
	return resp["value"]
}

func TestRevealIntegrationSecret(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{"integrations": json.RawMessage(maskingIntegrationsSeed)}}
	srv := newMaskingServer(store)
	for path, want := range revealPlainSeeds {
		w := postReveal(srv, "/api/integrations/secret", `{"field":"`+path+`"}`)
		if got := revealValue(t, w); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
		for _, other := range revealPlainSeeds {
			if other != want && strings.Contains(w.Body.String(), other) {
				t.Errorf("%s response contains %q", path, other)
			}
		}
	}
}

func TestRevealIntegrationSecretEnvAndUnset(t *testing.T) {
	store := &fakeStore{}
	srv := withTestSession(New(store, func() {}, func() config.App {
		cfg := testEffective()
		cfg.Notify.TelegramToken = "ENV-tg"
		if err := settings.ApplyOverrides(context.Background(), &cfg, store); err != nil {
			panic(err)
		}
		return cfg
	}, ":0", runner.NewState(), nil))
	if got := revealValue(t, postReveal(srv, "/api/integrations/secret", `{"field":"notifications.telegram.bot_token"}`)); got != "ENV-tg" {
		t.Errorf("env token = %q, want ENV-tg", got)
	}
	w := postReveal(srv, "/api/integrations/secret", `{"field":"metrics.token"}`)
	if got := revealValue(t, w); got != "" {
		t.Errorf("unset metrics.token = %q, want empty", got)
	}
	if strings.TrimSpace(w.Body.String()) != `{"value":""}` {
		t.Errorf("unset body = %s", w.Body.String())
	}
}

func TestRevealIntegrationSecretRejects(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{"integrations": json.RawMessage(maskingIntegrationsSeed)}}
	srv := newMaskingServer(store)
	for _, body := range []string{
		`{"field":"qbit.host"}`, `{"field":"notifications.webhook.url"}`,
		`{"field":"nope"}`, `{}`, `{"field":`,
	} {
		w := postReveal(srv, "/api/integrations/secret", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400, body: %s", body, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "SEC-") {
			t.Errorf("%s: error body leaks a secret: %s", body, w.Body.String())
		}
	}
}

func TestRevealTrackerSecret(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"credentials":"` + maskingTrackerCreds + `","cookies":"SEC-trc"},"nnm":{"credentials":"SEC-nnmc","cookies":"SEC-nnm"}}`),
	}}
	srv := newMaskingServer(store)
	if got := revealValue(t, postReveal(srv, "/api/trackers/rutracker/secret", `{"field":"password"}`)); got != "SEC-trp" {
		t.Errorf("password = %q, want SEC-trp", got)
	}
	if got := revealValue(t, postReveal(srv, "/api/trackers/nnm/secret", `{"field":"credentials"}`)); got != "SEC-nnmc" {
		t.Errorf("nnm credentials = %q, want SEC-nnmc", got)
	}
	for _, tc := range []struct {
		url, body string
		want      int
	}{
		{"/api/trackers/rutracker/secret", `{"field":"credentials"}`, http.StatusBadRequest},
		{"/api/trackers/rutracker/secret", `{"field":"cookies"}`, http.StatusBadRequest},
		{"/api/trackers/rutracker/secret", `{"field":"nope"}`, http.StatusBadRequest},
		{"/api/trackers/rutracker/secret", `{"field":`, http.StatusBadRequest},
		{"/api/trackers/nnm/secret", `{"field":"password"}`, http.StatusBadRequest},
		{"/api/trackers/nosuchtracker/secret", `{"field":"password"}`, http.StatusNotFound},
	} {
		w := postReveal(srv, tc.url, tc.body)
		if w.Code != tc.want {
			t.Errorf("%s %s: status = %d, want %d, body: %s", tc.url, tc.body, w.Code, tc.want, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "SEC-") {
			t.Errorf("%s %s: error body leaks a secret: %s", tc.url, tc.body, w.Body.String())
		}
	}
}

func TestRevealRequiresAuth(t *testing.T) {
	routes := []struct{ url, body string }{
		{"/api/integrations/secret", `{"field":"metrics.token"}`},
		{"/api/trackers/rutracker/secret", `{"field":"password"}`},
	}
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	cookie := loginCookie(t, srv)
	for _, rt := range routes {
		req := httptest.NewRequest(http.MethodPost, rt.url, strings.NewReader(rt.body))
		req.Header.Set("X-Ripperr", "1")
		req.RemoteAddr = "192.168.1.10:1234"
		req.Host = "192.168.1.5:6410"
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s from LAN without cookie: status = %d, want 401", rt.url, w.Code)
		}

		req = httptest.NewRequest(http.MethodPost, rt.url, strings.NewReader(rt.body))
		req.Header.Set("X-Ripperr", "1")
		req.RemoteAddr = "192.168.1.10:1234"
		req.Host = "192.168.1.5:6410"
		req.AddCookie(cookie)
		w = httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s with cookie: status = %d, want 200, body: %s", rt.url, w.Code, w.Body.String())
		}

		req = httptest.NewRequest(http.MethodPost, rt.url, strings.NewReader(rt.body))
		req.Header.Set("X-Ripperr", "1")
		req.RemoteAddr = "192.168.1.10:1234"
		req.Host = "192.168.1.5:6410"
		req.AddCookie(cookie)
		req.Header.Set("Origin", "http://evil.example")
		w = httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s cross-origin: status = %d, want 403", rt.url, w.Code)
		}
	}
}

func TestRevealedLiteralSaveKeepsStored(t *testing.T) {
	stubTelegramResolver(t)
	seed := json.RawMessage(maskingIntegrationsSeed)
	store := &fakeStore{settings: map[string]json.RawMessage{"integrations": seed}}
	srv := newMaskingServer(store)

	var doc map[string]any
	if err := json.Unmarshal(getIntegrations(t, srv), &doc); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"qbit.password", "metrics.token"} {
		value := revealValue(t, postReveal(srv, "/api/integrations/secret", `{"field":"`+path+`"}`))
		parts := strings.Split(path, ".")
		doc[parts[0]].(map[string]any)[parts[1]] = value
	}
	body, _ := json.Marshal(doc)
	if w := putIntegrations(srv, body); w.Code != http.StatusNoContent {
		t.Fatalf("PUT integrations status = %d, body: %s", w.Code, w.Body.String())
	}
	for _, p := range maskingSecretPaths {
		if got, want := jsonPath(t, store.settings["integrations"], p), jsonPath(t, seed, p); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}

	tstore := newMaskingTrackerStore()
	before := string(tstore.settings["trackers"])
	tsrv := newMaskingServer(tstore)
	password := revealValue(t, postReveal(tsrv, "/api/trackers/rutracker/secret", `{"field":"password"}`))
	tbody, _ := json.Marshal(map[string]any{"login": "vasya", "password": password, "credentials": settings.SecretMask, "cookies": settings.SecretMask})
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/rutracker", bytes.NewReader(tbody))
	req.SetPathValue("key", "rutracker")
	w := httptest.NewRecorder()
	tsrv.handlePutTracker(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("PUT tracker status = %d, body: %s", w.Code, w.Body.String())
	}
	var saved map[string]map[string]any
	if err := json.Unmarshal(tstore.settings["trackers"], &saved); err != nil {
		t.Fatal(err)
	}
	assertStoredTemplate(t, tstore, "rutracker", maskingTrackerTemplate, maskingTrackerCreds)
	if saved["rutracker"]["cookies"] != "SEC-trc" {
		t.Errorf("stored = %v, want seeds from %s", saved["rutracker"], before)
	}
}

func TestRevealTrackerCredentialsOnlyRaw(t *testing.T) {
	seeds := map[string]string{
		"legacy":   `{"rutracker":{"credentials":"` + maskingTrackerCreds + `"},"nnm":{"credentials":"SEC-nnmraw"}}`,
		"template": `{"rutracker":{"credentials":"` + maskingTrackerTemplate + `","login":"vasya","password":"SEC-trp"},"nnm":{"credentials":"SEC-nnmraw"}}`,
	}
	for name, seed := range seeds {
		srv := newMaskingServer(&fakeStore{settings: map[string]json.RawMessage{"trackers": json.RawMessage(seed)}})
		w := postReveal(srv, "/api/trackers/rutracker/secret", `{"field":"credentials"}`)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "SEC-") {
			t.Errorf("%s: rutracker credentials = %d %s, want 400 without secrets", name, w.Code, w.Body.String())
		}
		if got := revealValue(t, postReveal(srv, "/api/trackers/nnm/secret", `{"field":"credentials"}`)); got != "SEC-nnmraw" {
			t.Errorf("%s: nnm credentials = %q, want SEC-nnmraw", name, got)
		}
		if got := revealValue(t, postReveal(srv, "/api/trackers/rutracker/secret", `{"field":"password"}`)); got != "SEC-trp" {
			t.Errorf("%s: rutracker password = %q, want SEC-trp", name, got)
		}
	}
}
