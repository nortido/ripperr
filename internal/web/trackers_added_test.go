package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/runner"
	"ripperr/internal/settings"
	"ripperr/internal/tracker/registry"
)

func newAddedServer(store *fakeStore, reloads *int) *Server {
	return withTestSession(New(store, func() { *reloads++ }, func() config.App {
		cfg := testEffective()
		cfg.Tracker = registry.ConfigDefaults()
		cfg.Runner.TLSProfile = "chrome_131"
		if err := settings.ApplyOverrides(context.Background(), &cfg, store); err != nil {
			panic(err)
		}
		return cfg
	}, ":0", runner.NewState(), nil))
}

func serveAdded(srv *Server, method, url, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("X-Ripperr", "1")
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

func trackerListKeys(t *testing.T, srv *Server) []string {
	t.Helper()
	w := serveAdded(srv, http.MethodGet, "/api/trackers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/trackers status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp []struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(resp))
	for _, it := range resp {
		out = append(out, it.Key)
	}
	return out
}

func storedStrings(t *testing.T, store *fakeStore, key string) []string {
	t.Helper()
	raw, ok := store.settings[key]
	if !ok {
		t.Fatalf("document %s missing", key)
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func storedOverlay(t *testing.T, store *fakeStore) map[string]map[string]any {
	t.Helper()
	var out map[string]map[string]any
	if raw, ok := store.settings["trackers"]; ok {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestGetTrackersListsOnlyAdded(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers_added": json.RawMessage(`["kinozal","rutracker"]`),
	}}
	var reloads int
	srv := newAddedServer(store, &reloads)
	if got := trackerListKeys(t, srv); !slices.Equal(got, []string{"kinozal", "rutracker"}) {
		t.Fatalf("keys = %v, want [kinozal rutracker]", got)
	}
	store.settings["trackers_order"] = json.RawMessage(`["rutracker","nnm"]`)
	if got := trackerListKeys(t, srv); !slices.Equal(got, []string{"rutracker", "kinozal"}) {
		t.Fatalf("keys with order = %v, want [rutracker kinozal]", got)
	}
	if reloads != 0 {
		t.Errorf("GET reloaded %d times", reloads)
	}
}

func TestGetTrackerTypes(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers_added": json.RawMessage(`["rutracker","nnm"]`),
		"trackers":       json.RawMessage(`{"kinozal":{"password":"SEC-t"},"nnm":{"credentials":"SEC-nnm"}}`),
	}}
	var reloads int
	srv := newAddedServer(store, &reloads)
	w := serveAdded(srv, http.MethodGet, "/api/trackers/types", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "SEC-") {
		t.Fatalf("types leak a secret: %s", w.Body.String())
	}
	var resp []struct {
		Key         string `json:"key"`
		Added       bool   `json:"added"`
		Credentials string `json:"credentials"`
		Password    string `json:"password"`
		Domain      string `json:"domain"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp) != len(registry.Keys()) {
		t.Fatalf("types = %d, want %d", len(resp), len(registry.Keys()))
	}
	for _, it := range resp {
		want := it.Key == "rutracker" || it.Key == "nnm"
		if it.Added != want {
			t.Errorf("%s added = %v, want %v", it.Key, it.Added, want)
		}
		if it.Key == "kinozal" {
			if it.Credentials != "username={login}&password={password}&to=%2F" {
				t.Errorf("kinozal credentials = %q", it.Credentials)
			}
			if it.Password != settings.SecretMask {
				t.Errorf("kinozal password = %q, want mask", it.Password)
			}
			if it.Domain != "kinozal.tv" {
				t.Errorf("kinozal domain = %q", it.Domain)
			}
		}
	}
	if reloads != 0 {
		t.Errorf("GET reloaded %d times", reloads)
	}
}

func TestAddTracker(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers_added": json.RawMessage(`["rutracker"]`),
	}}
	var reloads int
	srv := newAddedServer(store, &reloads)
	w := serveAdded(srv, http.MethodPost, "/api/trackers/toloka", `{"login":"u","password":"p","domain":"toloka.to"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("add status = %d, body: %s", w.Code, w.Body.String())
	}
	if got := storedStrings(t, store, "trackers_added"); !slices.Equal(got, []string{"rutracker", "toloka"}) {
		t.Errorf("added = %v, want [rutracker toloka]", got)
	}
	toloka := storedOverlay(t, store)["toloka"]
	if toloka["enabled"] != true || toloka["login"] != "u" || toloka["password"] != "p" {
		t.Errorf("toloka overlay = %v, want enabled, login u, password p", toloka)
	}
	if creds, _ := toloka["credentials"].(string); !strings.Contains(creds, "username={login}") || !strings.Contains(creds, "password={password}") {
		t.Errorf("toloka credentials = %q, want a template", creds)
	}
	if reloads != 1 {
		t.Errorf("reloads = %d, want 1", reloads)
	}
	if got := trackerListKeys(t, srv); !slices.Equal(got, []string{"rutracker", "toloka"}) {
		t.Errorf("list = %v", got)
	}

	if w = serveAdded(srv, http.MethodPost, "/api/trackers/toloka", `{}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "tracker already added") {
		t.Errorf("repeat add = %d %s, want 409", w.Code, w.Body.String())
	}
	if w = serveAdded(srv, http.MethodPost, "/api/trackers/bogus", `{}`); w.Code != http.StatusNotFound {
		t.Errorf("bogus add = %d, want 404", w.Code)
	}
	beforeList, beforeOverlay := string(store.settings["trackers_added"]), string(store.settings["trackers"])
	w = serveAdded(srv, http.MethodPost, "/api/trackers/anidub", `{"domain":"evil.example"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "domain is read-only") {
		t.Errorf("foreign domain add = %d %s, want 400 domain is read-only", w.Code, w.Body.String())
	}
	if string(store.settings["trackers_added"]) != beforeList || string(store.settings["trackers"]) != beforeOverlay {
		t.Error("rejected add changed the store")
	}
	if reloads != 1 {
		t.Errorf("reloads = %d after rejected adds, want 1", reloads)
	}
}

func TestRemoveTracker(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers_added": json.RawMessage(`["rutracker","kinozal"]`),
		"trackers":       json.RawMessage(`{"kinozal":{"credentials":"username={login}&password={password}&to=%2F","login":"SEC-l","password":"SEC-p","cookies":"SEC-c"},"rutracker":{"hour_limit":5}}`),
		"trackers_order": json.RawMessage(`["kinozal","rutracker"]`),
	}}
	var reloads int
	srv := newAddedServer(store, &reloads)
	if w := serveAdded(srv, http.MethodDelete, "/api/trackers/kinozal", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body: %s", w.Code, w.Body.String())
	}
	if got := storedStrings(t, store, "trackers_added"); !slices.Equal(got, []string{"rutracker"}) {
		t.Errorf("added = %v, want [rutracker]", got)
	}
	overlay := storedOverlay(t, store)
	if _, ok := overlay["kinozal"]; ok {
		t.Errorf("overlay still has kinozal: %v", overlay)
	}
	if overlay["rutracker"]["hour_limit"] != float64(5) {
		t.Errorf("rutracker overlay = %v, want untouched", overlay["rutracker"])
	}
	if !slices.Equal(store.deletedTrackers, []int{registry.ID("kinozal")}) || registry.ID("kinozal") != 2 {
		t.Errorf("DeleteTracker calls = %v, want [2]", store.deletedTrackers)
	}
	if got := storedStrings(t, store, "trackers_order"); !slices.Equal(got, []string{"rutracker"}) {
		t.Errorf("order = %v, want [rutracker]", got)
	}
	if reloads != 1 {
		t.Errorf("reloads = %d, want 1", reloads)
	}

	cases := []struct {
		method, url, body string
		want              int
	}{
		{http.MethodPut, "/api/trackers/kinozal", `{"hour_limit":3}`, http.StatusNotFound},
		{http.MethodPost, "/api/trackers/kinozal/secret", `{"field":"password"}`, http.StatusNotFound},
		{http.MethodPut, "/api/trackers/order", `{"order":["rutracker","kinozal"]}`, http.StatusBadRequest},
		{http.MethodDelete, "/api/trackers/kinozal", "", http.StatusNotFound},
	}
	for _, tc := range cases {
		if w := serveAdded(srv, tc.method, tc.url, tc.body); w.Code != tc.want {
			t.Errorf("%s %s = %d %s, want %d", tc.method, tc.url, w.Code, w.Body.String(), tc.want)
		}
	}
	if w := serveAdded(srv, http.MethodPut, "/api/trackers/order", `{"order":["kinozal"]}`); !strings.Contains(w.Body.String(), "unknown tracker key: kinozal") {
		t.Errorf("order error = %s", w.Body.String())
	}
	if reloads != 1 || len(store.deletedTrackers) != 1 {
		t.Errorf("rejected calls changed state: reloads %d, deletes %v", reloads, store.deletedTrackers)
	}
	// проверка входа работает и для недобавленного трекера: ее зовет диалог добавления
	w := serveAdded(srv, http.MethodPost, "/api/trackers/kinozal/test", `{"config":{"publicity":"public","active_domain":"kinozal.tv"}}`)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Errorf("test of removed tracker = %d %s, want 200 ok", w.Code, w.Body.String())
	}
}

func TestPutTrackerDomainReadOnly(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"hour_limit":5}}`),
	}}
	var reloads int
	srv := newAddedServer(store, &reloads)
	before := string(store.settings["trackers"])
	for _, body := range []string{`{"domain":"evil.example"}`, `{"domain":"RUTRACKER.ORG.evil"}`, `{"domain":"","hour_limit":3}`} {
		w := serveAdded(srv, http.MethodPut, "/api/trackers/rutracker", body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "domain is read-only") {
			t.Errorf("PUT %s = %d %s, want 400 domain is read-only", body, w.Code, w.Body.String())
		}
	}
	if string(store.settings["trackers"]) != before || reloads != 0 {
		t.Fatalf("rejected PUT changed the store: %s, reloads %d", store.settings["trackers"], reloads)
	}
	for _, body := range []string{`{"domain":"rutracker.org"}`, `{"domain":" rutracker.org ","hour_limit":7}`} {
		if w := serveAdded(srv, http.MethodPut, "/api/trackers/rutracker", body); w.Code != http.StatusNoContent {
			t.Errorf("PUT %s = %d %s, want 204", body, w.Code, w.Body.String())
		}
	}
	if got := storedOverlay(t, store)["rutracker"]["domain"]; got != "rutracker.org" {
		t.Errorf("stored domain = %q, want rutracker.org without spaces", got)
	}
	w := serveAdded(srv, http.MethodPost, "/api/trackers/rutracker/test", `{"config":{"domain":"evil.example","publicity":"public"}}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "domain is read-only") {
		t.Errorf("test with foreign domain = %d %s, want 400", w.Code, w.Body.String())
	}
}

func TestTrackerMembershipRoutesNeedSameOrigin(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers_added": json.RawMessage(`["rutracker"]`),
	}}
	var reloads int
	srv := newAddedServer(store, &reloads)
	before := string(store.settings["trackers_added"])
	for _, tc := range []struct{ method, url, body string }{
		{http.MethodPost, "/api/trackers/kinozal", `{}`},
		{http.MethodDelete, "/api/trackers/rutracker", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.url, strings.NewReader(tc.body))
		req.Header.Set("Origin", "https://evil.example")
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s cross-origin = %d, want 403", tc.method, tc.url, w.Code)
		}
	}
	if string(store.settings["trackers_added"]) != before || reloads != 0 || len(store.deletedTrackers) != 0 {
		t.Error("cross-origin request changed state")
	}
}

func TestTrackersLegacyWithoutAddedList(t *testing.T) {
	store := &fakeStore{}
	var reloads int
	srv := newAddedServer(store, &reloads)
	if got := trackerListKeys(t, srv); !slices.Equal(got, registry.Keys()) {
		t.Fatalf("legacy list = %v, want every registry key", got)
	}
	if w := serveAdded(srv, http.MethodPost, "/api/trackers/kinozal", `{}`); w.Code != http.StatusConflict {
		t.Errorf("legacy add = %d %s, want 409", w.Code, w.Body.String())
	}
	if _, ok := store.settings["trackers_added"]; ok {
		t.Error("409 created the added list")
	}
	if w := serveAdded(srv, http.MethodDelete, "/api/trackers/nnm", ""); w.Code != http.StatusNoContent {
		t.Fatalf("legacy delete = %d %s, want 204", w.Code, w.Body.String())
	}
	want := slices.DeleteFunc(registry.Keys(), func(k string) bool { return k == "nnm" })
	if got := storedStrings(t, store, "trackers_added"); !slices.Equal(got, want) {
		t.Errorf("added = %v, want %v", got, want)
	}
}
