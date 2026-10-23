package web

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func storedMirrors(t *testing.T, store *fakeStore, key string) []string {
	t.Helper()
	raw, _ := storedOverlay(t, store)[key]["mirrors"].([]any)
	out := make([]string, 0, len(raw))
	for _, m := range raw {
		s, _ := m.(string)
		out = append(out, s)
	}
	return out
}

func TestPutTrackerDomainFromMirrors(t *testing.T) {
	for _, tc := range []struct {
		name, stored, body string
		wantDomain         string
		wantMirrors        []string
		wantActive         any
	}{
		{"plain", `{"rutracker":{"hour_limit":5}}`, `{"domain":"rutracker.net"}`, "rutracker.net", []string{"rutracker.org"}, nil},
		{"case and spaces", `{"rutracker":{"hour_limit":5}}`, `{"domain":" RuTracker.NET "}`, "rutracker.net", []string{"rutracker.org"}, nil},
		{"stored active kept as main", `{"rutracker":{"active_domain":"rutracker.net"}}`, `{"domain":"rutracker.net"}`, "rutracker.net", []string{"rutracker.org"}, "rutracker.net"},
		{"sent active kept as main", `{}`, `{"domain":"rutracker.net","active_domain":"RUTRACKER.NET"}`, "rutracker.net", []string{"rutracker.org"}, "rutracker.net"},
		{"other active kept", `{"rutracker":{"active_domain":"rutracker.org"}}`, `{"domain":"rutracker.net"}`, "rutracker.net", []string{"rutracker.org"}, "rutracker.org"},
		{"sent mirrors", `{}`, `{"domain":"evil.example","mirrors":["evil.example","rutracker.net"]}`, "evil.example", []string{"rutracker.net", "rutracker.org"}, nil},
		{"old domain already a mirror", `{}`, `{"domain":"b.example","mirrors":["RUTRACKER.ORG","b.example"]}`, "b.example", []string{"RUTRACKER.ORG"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{settings: map[string]json.RawMessage{"trackers": json.RawMessage(tc.stored)}}
			var reloads int
			srv := newAddedServer(store, &reloads)
			if w := serveAdded(srv, http.MethodPut, "/api/trackers/rutracker", tc.body); w.Code != http.StatusNoContent {
				t.Fatalf("PUT %s = %d %s, want 204", tc.body, w.Code, w.Body.String())
			}
			item := storedOverlay(t, store)["rutracker"]
			if item["domain"] != tc.wantDomain {
				t.Errorf("domain = %v, want %s", item["domain"], tc.wantDomain)
			}
			if got := storedMirrors(t, store, "rutracker"); !slices.Equal(got, tc.wantMirrors) {
				t.Errorf("mirrors = %v, want %v", got, tc.wantMirrors)
			}
			if item["active_domain"] != tc.wantActive {
				t.Errorf("active_domain = %#v, want %#v", item["active_domain"], tc.wantActive)
			}
		})
	}
}

func TestPutTrackerDomainNotMirrorRejected(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"hour_limit":5}}`),
	}}
	var reloads int
	srv := newAddedServer(store, &reloads)
	before := string(store.settings["trackers"])
	for _, body := range []string{
		`{"domain":"evil.example"}`,
		`{"domain":"rutracker.net","mirrors":["other.example"]}`,
		`{"domain":"rutracker.net","mirrors":[]}`,
	} {
		w := serveAdded(srv, http.MethodPut, "/api/trackers/rutracker", body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "domain is read-only") {
			t.Errorf("PUT %s = %d %s, want 400 domain is read-only", body, w.Code, w.Body.String())
		}
	}
	if string(store.settings["trackers"]) != before || reloads != 0 {
		t.Fatalf("rejected PUT changed the store: %s, reloads %d", store.settings["trackers"], reloads)
	}
}

func TestAddTrackerDomainFromMirrors(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers_added": json.RawMessage(`["rutracker"]`),
	}}
	var reloads int
	srv := newAddedServer(store, &reloads)
	if w := serveAdded(srv, http.MethodPost, "/api/trackers/kinozal", `{"domain":"evil.example"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("add with foreign domain = %d %s, want 400", w.Code, w.Body.String())
	}
	if w := serveAdded(srv, http.MethodPost, "/api/trackers/kinozal", `{"domain":"kinozal.guru"}`); w.Code != http.StatusNoContent {
		t.Fatalf("add with mirror domain = %d %s, want 204", w.Code, w.Body.String())
	}
	if got := storedOverlay(t, store)["kinozal"]["domain"]; got != "kinozal.guru" {
		t.Errorf("domain = %v, want kinozal.guru", got)
	}
	if got, want := storedMirrors(t, store, "kinozal"), []string{"kinozal.me", "kinozal.tv"}; !slices.Equal(got, want) {
		t.Errorf("mirrors = %v, want %v", got, want)
	}
}

func TestTestTrackerDomainFromMirrors(t *testing.T) {
	var reloads int
	srv := newAddedServer(&fakeStore{settings: map[string]json.RawMessage{}}, &reloads)
	w := serveAdded(srv, http.MethodPost, "/api/trackers/rutracker/test", `{"config":{"domain":"rutracker.net","active_domain":"rutracker.org","publicity":"public"}}`)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Errorf("test with mirror domain = %d %s, want 200 ok", w.Code, w.Body.String())
	}
	w = serveAdded(srv, http.MethodPost, "/api/trackers/rutracker/test", `{"config":{"domain":"evil.example","publicity":"public"}}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("test with foreign domain = %d %s, want 400", w.Code, w.Body.String())
	}
}

func TestTrackerPatchRejectsFoldedKeys(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers":       json.RawMessage(`{"rutracker":{"hour_limit":5,"cookies":"real"}}`),
		"trackers_added": json.RawMessage(`["rutracker"]`),
	}}
	var reloads int
	srv := newAddedServer(store, &reloads)
	before := string(store.settings["trackers"])
	for _, tc := range []struct{ method, url, body string }{
		{http.MethodPut, "/api/trackers/rutracker", `{"Domain":"evil.example"}`},
		{http.MethodPut, "/api/trackers/rutracker", `{"domain":"rutracker.org","Domain":"evil.example"}`},
		{http.MethodPut, "/api/trackers/rutracker", `{"Cookies":"********"}`},
		{http.MethodPut, "/api/trackers/rutracker", `{"ACTIVE_DOMAIN":"evil.example"}`},
		{http.MethodPost, "/api/trackers/kinozal", `{"DOMAIN":"evil.example"}`},
		{http.MethodPost, "/api/trackers/rutracker/test", `{"config":{"Domain":"evil.example","publicity":"public"}}`},
	} {
		w := serveAdded(srv, tc.method, tc.url, tc.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s %s = %d %s, want 400", tc.method, tc.url, tc.body, w.Code, w.Body.String())
		}
	}
	if string(store.settings["trackers"]) != before || reloads != 0 {
		t.Fatalf("rejected patch changed the store: %s, reloads %d", store.settings["trackers"], reloads)
	}
	if got := storedStrings(t, store, "trackers_added"); !slices.Equal(got, []string{"rutracker"}) {
		t.Fatalf("trackers_added = %v, want [rutracker]", got)
	}
}

func TestTrackerDomainSwapMovesURLs(t *testing.T) {
	for _, tc := range []struct {
		name, method, url, stored, body string
		key, wantLogin, wantDownload    string
	}{
		{"rutracker defaults", http.MethodPut, "/api/trackers/rutracker", `{}`, `{"domain":"rutracker.net"}`,
			"rutracker", "https://rutracker.net/forum/login.php", "https://rutracker.net/forum/dl.php?t="},
		{"rutracker old urls sent", http.MethodPut, "/api/trackers/rutracker", `{}`,
			`{"domain":"rutracker.net","login_url":"https://RUTRACKER.ORG/forum/login.php?x=1","download_url":"https://rutracker.org/forum/dl.php?t="}`,
			"rutracker", "https://rutracker.net/forum/login.php?x=1", "https://rutracker.net/forum/dl.php?t="},
		{"rutracker third host kept", http.MethodPut, "/api/trackers/rutracker", `{"rutracker":{"login_url":"https://auth.example/login"}}`, `{"domain":"rutracker.net"}`,
			"rutracker", "https://auth.example/login", "https://rutracker.net/forum/dl.php?t="},
		{"kinozal add", http.MethodPost, "/api/trackers/kinozal", `{}`, `{"domain":"kinozal.me"}`,
			"kinozal", "https://kinozal.me/takelogin.php", "https://kinozal.me/download.php?id="},
		{"kinozal add old urls sent", http.MethodPost, "/api/trackers/kinozal", `{}`,
			`{"domain":"kinozal.guru","login_url":"https://kinozal.tv/takelogin.php","download_url":"https://kinozal.tv/download.php?id="}`,
			"kinozal", "https://kinozal.guru/takelogin.php", "https://kinozal.guru/download.php?id="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{settings: map[string]json.RawMessage{
				"trackers":       json.RawMessage(tc.stored),
				"trackers_added": json.RawMessage(`["rutracker"]`),
			}}
			var reloads int
			srv := newAddedServer(store, &reloads)
			if w := serveAdded(srv, tc.method, tc.url, tc.body); w.Code != http.StatusNoContent {
				t.Fatalf("%s %s = %d %s, want 204", tc.method, tc.body, w.Code, w.Body.String())
			}
			w := serveAdded(srv, http.MethodGet, "/api/trackers", "")
			type urlView struct {
				Key         string `json:"key"`
				LoginURL    string `json:"login_url"`
				DownloadURL string `json:"download_url"`
			}
			var list []urlView
			if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
				t.Fatal(err)
			}
			i := slices.IndexFunc(list, func(it urlView) bool { return it.Key == tc.key })
			if i < 0 {
				t.Fatalf("%s missing from GET /api/trackers", tc.key)
			}
			if list[i].LoginURL != tc.wantLogin || list[i].DownloadURL != tc.wantDownload {
				t.Errorf("urls = %q %q, want %q %q", list[i].LoginURL, list[i].DownloadURL, tc.wantLogin, tc.wantDownload)
			}
		})
	}
}

func TestPutTrackerActiveDomainEqualToMainKept(t *testing.T) {
	for _, tc := range []struct {
		name, stored, body string
		wantDomain         string
		wantActive         string
		wantMirrors        []string
	}{
		{"swap with switch off", `{"rutracker":{"mirrors":["rutracker.net","m.example"]}}`, `{"domain":"m.example","active_domain":"m.example","mirrors":["rutracker.net","m.example"]}`,
			"m.example", "m.example", []string{"rutracker.net", "rutracker.org"}},
		{"current domain as active", `{}`, `{"active_domain":"rutracker.org"}`, "", "rutracker.org", []string{"rutracker.net"}},
		{"empty is auto", `{"rutracker":{"active_domain":"rutracker.org"}}`, `{"active_domain":""}`, "", "", []string{"rutracker.net"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{settings: map[string]json.RawMessage{"trackers": json.RawMessage(tc.stored)}}
			var reloads int
			srv := newAddedServer(store, &reloads)
			if w := serveAdded(srv, http.MethodPut, "/api/trackers/rutracker", tc.body); w.Code != http.StatusNoContent {
				t.Fatalf("PUT %s = %d %s, want 204", tc.body, w.Code, w.Body.String())
			}
			item := storedOverlay(t, store)["rutracker"]
			if tc.wantDomain != "" && item["domain"] != tc.wantDomain {
				t.Errorf("domain = %v, want %s", item["domain"], tc.wantDomain)
			}
			if item["active_domain"] != tc.wantActive {
				t.Errorf("active_domain = %#v, want %q", item["active_domain"], tc.wantActive)
			}
			if got := effectiveMirrorsAfter(t, srv); !slices.Equal(got, tc.wantMirrors) {
				t.Errorf("mirrors = %v, want %v", got, tc.wantMirrors)
			}
		})
	}
}

func effectiveMirrorsAfter(t *testing.T, srv *Server) []string {
	t.Helper()
	cfg := srv.effective()
	return cfg.Tracker.ByKey("rutracker").Mirrors
}

func TestTrackerDomainSwapDropsShownMain(t *testing.T) {
	for _, tc := range []struct {
		name, method, key, body string
		wantDomain              string
		wantMirrors             []string
		wantRetired             []string
	}{
		{"shown main removed", http.MethodPut, "rutracker",
			`{"domain":"rutracker.net","mirrors":["rutracker.net","b.example"],"mirrors_seen":["rutracker.net"," RuTracker.ORG "]}`,
			"rutracker.net", []string{"b.example"}, []string{"rutracker.org"}},
		{"shown main kept", http.MethodPut, "rutracker",
			`{"domain":"rutracker.net","mirrors":["b.example","rutracker.net","rutracker.org"],"mirrors_seen":["rutracker.net","rutracker.org"]}`,
			"rutracker.net", []string{"b.example", "rutracker.org"}, []string{}},
		{"main not shown", http.MethodPut, "rutracker",
			`{"domain":"rutracker.net","mirrors":["rutracker.net"],"mirrors_seen":["rutracker.net"]}`,
			"rutracker.net", []string{"rutracker.org"}, nil},
		{"mirrors not sent", http.MethodPut, "rutracker",
			`{"domain":"rutracker.net","mirrors_seen":["rutracker.org","rutracker.net"]}`,
			"rutracker.net", []string{"rutracker.org"}, nil},
		{"add flow", http.MethodPost, "kinozal",
			`{"domain":"kinozal.guru","mirrors":["kinozal.me","kinozal.guru"],"mirrors_seen":["kinozal.me","kinozal.guru","kinozal.tv"]}`,
			"kinozal.guru", []string{"kinozal.me"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := map[string]json.RawMessage{"trackers": json.RawMessage(`{"rutracker":{"hour_limit":5}}`)}
			if tc.method == http.MethodPost {
				docs = map[string]json.RawMessage{"trackers_added": json.RawMessage(`["rutracker"]`)}
			}
			store := &fakeStore{settings: docs}
			var reloads int
			srv := newAddedServer(store, &reloads)
			if w := serveAdded(srv, tc.method, "/api/trackers/"+tc.key, tc.body); w.Code != http.StatusNoContent {
				t.Fatalf("%s %s = %d %s, want 204", tc.method, tc.body, w.Code, w.Body.String())
			}
			if got := storedOverlay(t, store)[tc.key]["domain"]; got != tc.wantDomain {
				t.Errorf("domain = %v, want %s", got, tc.wantDomain)
			}
			if got := storedMirrors(t, store, tc.key); !slices.Equal(got, tc.wantMirrors) {
				t.Errorf("mirrors = %v, want %v", got, tc.wantMirrors)
			}
			if tc.wantRetired != nil {
				if got, _ := storedTrackerList(t, store, tc.key, "retired"); !slices.Equal(got, tc.wantRetired) {
					t.Errorf("retired = %v, want %v", got, tc.wantRetired)
				}
			}
		})
	}
}
