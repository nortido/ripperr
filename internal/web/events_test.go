package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"ripperr/internal/model/storage"
)

type fakeEvents struct {
	mu           sync.Mutex
	items        []storage.EventEntry
	added        []storage.EventEntry
	lastLimit    int
	lastSubjects []string
	lastKinds    []string
}

func (f *fakeEvents) AddEvent(_ context.Context, e storage.EventEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.added = append(f.added, e)
	return nil
}

func (f *fakeEvents) ListEvents(_ context.Context, limit int) ([]storage.EventEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastLimit = limit
	return f.items, nil
}

func (f *fakeEvents) ListSubjectEvents(_ context.Context, subjects, kinds []string, limit int) ([]storage.EventEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastSubjects = subjects
	f.lastKinds = kinds
	var out []storage.EventEntry
	for _, e := range f.items {
		if slices.Contains(subjects, e.Subject) && slices.Contains(kinds, e.Kind) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func newEventsServer(fe *fakeEvents) *Server {
	srv := withTestSession(newBareServer(&fakeStore{}))
	srv.SetEvents(fe)
	return srv
}

func doEvents(srv *Server, method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rec, req)
	return rec
}

func TestGetEventsNewestFirstAndLimit(t *testing.T) {
	at := time.Date(2026, 10, 25, 1, 2, 3, 0, time.UTC)
	fe := &fakeEvents{items: []storage.EventEntry{
		{ID: 2, At: at, Source: "server", Kind: "updated", Text: "new", Subject: "Rel", AppPath: "#dashboard"},
		{ID: 1, At: at.Add(-time.Minute), Source: "ui", Kind: "ui_error", Text: "old"},
	}}
	srv := newEventsServer(fe)

	rec := doEvents(srv, http.MethodGet, "/api/events", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 || got.Items[0]["id"] != float64(2) || got.Items[1]["id"] != float64(1) {
		t.Fatalf("items %v", got.Items)
	}
	first := got.Items[0]
	if first["time"] != at.Format(time.RFC3339) || first["source"] != "server" || first["kind"] != "updated" ||
		first["text"] != "new" || first["subject"] != "Rel" || first["app_path"] != "#dashboard" {
		t.Fatalf("fields %v", first)
	}
	if fe.lastLimit != 200 {
		t.Fatalf("default limit %d", fe.lastLimit)
	}

	for target, want := range map[string]int{"/api/events?limit=0": 200, "/api/events?limit=-5": 200, "/api/events?limit=9999": 500, "/api/events?limit=7": 7} {
		doEvents(srv, http.MethodGet, target, "", nil)
		if fe.lastLimit != want {
			t.Fatalf("%s: limit %d, want %d", target, fe.lastLimit, want)
		}
	}
}

func TestGetEventsEmptyIsArray(t *testing.T) {
	for name, srv := range map[string]*Server{
		"empty store": newEventsServer(&fakeEvents{}),
		"nil store":   withTestSession(newBareServer(&fakeStore{})),
	} {
		rec := doEvents(srv, http.MethodGet, "/api/events", "", nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"items":[]`) {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestPostEventStoresUIError(t *testing.T) {
	fe := &fakeEvents{}
	srv := newEventsServer(fe)
	before := time.Now()
	rec := doEvents(srv, http.MethodPost, "/api/events", `{"text":" boom ","path":"#trackers"}`, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(fe.added) != 1 {
		t.Fatalf("added %d", len(fe.added))
	}
	e := fe.added[0]
	if e.Source != "ui" || e.Kind != "ui_error" || e.Text != "boom" || e.AppPath != "#trackers" || e.At.Before(before) {
		t.Fatalf("entry %+v", e)
	}
}

func TestPostEventCapsAndSanitizes(t *testing.T) {
	fe := &fakeEvents{}
	srv := newEventsServer(fe)
	post := func(body string) int {
		return doEvents(srv, http.MethodPost, "/api/events", body, nil).Code
	}

	long, _ := json.Marshal(map[string]string{"text": strings.Repeat("я", 1500)})
	if c := post(string(long)); c != http.StatusNoContent {
		t.Fatalf("long: %d", c)
	}
	if n := len([]rune(fe.added[0].Text)); n != 1000 {
		t.Fatalf("text runes %d", n)
	}

	for _, p := range []string{"javascript:alert(1)", "#a b", "#a\tb", "#" + strings.Repeat("x", 300)} {
		body, _ := json.Marshal(map[string]string{"text": "x", "path": p})
		if c := post(string(body)); c != http.StatusNoContent {
			t.Fatalf("%q: %d", p, c)
		}
		if got := fe.added[len(fe.added)-1].AppPath; got != "" {
			t.Fatalf("path %q kept as %q", p, got)
		}
	}

	n := len(fe.added)
	for _, body := range []string{`{"text":"   "}`, `{}`, `not json`} {
		if c := post(body); c != http.StatusBadRequest {
			t.Fatalf("%s: %d", body, c)
		}
	}
	if len(fe.added) != n {
		t.Fatalf("stored on bad input: %d", len(fe.added)-n)
	}
}

func TestPostEventRateLimit(t *testing.T) {
	fe := &fakeEvents{}
	srv := newEventsServer(fe)
	for i := 0; i < 20; i++ {
		if c := doEvents(srv, http.MethodPost, "/api/events", `{"text":"e"}`, nil).Code; c != http.StatusNoContent {
			t.Fatalf("post %d: %d", i+1, c)
		}
	}
	if c := doEvents(srv, http.MethodPost, "/api/events", `{"text":"e"}`, nil).Code; c != http.StatusTooManyRequests {
		t.Fatalf("21st: %d", c)
	}
	if len(fe.added) != 20 {
		t.Fatalf("added %d", len(fe.added))
	}
}

func TestPostEventNilStoreIsNoop(t *testing.T) {
	srv := withTestSession(newBareServer(&fakeStore{}))
	if c := doEvents(srv, http.MethodPost, "/api/events", `{"text":"e"}`, nil).Code; c != http.StatusNoContent {
		t.Fatalf("status %d", c)
	}
}

func TestPostEventNeedsCSRFHeader(t *testing.T) {
	fe := &fakeEvents{}
	srv := newBareServer(&fakeStore{})
	srv.SetEvents(fe)
	cookie := signSession(testSessionSecret, "admin", time.Now().Add(authSessionTTL))
	srv.authCache.set(&authDoc{Username: "admin", Secret: base64.RawURLEncoding.EncodeToString(testSessionSecret)})
	req := httptest.NewRequest(http.MethodPost, "/api/events", strings.NewReader(`{"text":"e"}`))
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: cookie})
	rec := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(fe.added) != 0 {
		t.Fatal("stored without X-Ripperr")
	}
}
