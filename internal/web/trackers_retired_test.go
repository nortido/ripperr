package web

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func storedTrackerList(t *testing.T, store *fakeStore, key, field string) ([]string, bool) {
	t.Helper()
	v, ok := storedOverlay(t, store)[key][field]
	if !ok {
		return nil, false
	}
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, m := range raw {
		s, _ := m.(string)
		out = append(out, s)
	}
	return out, true
}

func TestPutTrackerRetiredAndUnconfirmed(t *testing.T) {
	const withX = `{"rutracker":{"mirrors":["rutracker.net","x.example"],"unconfirmed":["x.example"]}}`
	for _, tc := range []struct {
		name, stored, body string
		wantMirrors        []string
		wantRetired        []string
		wantUnconfirmed    []string
		wantActive         any
		wantDomain         string
	}{
		{name: "removed mirror retired", stored: `{}`, body: `{"mirrors":[]}`,
			wantMirrors: []string{}, wantRetired: []string{"rutracker.net"}},
		{name: "retired order kept", stored: `{"rutracker":{"retired":["old.example"]}}`, body: `{"mirrors":[]}`,
			wantMirrors: []string{}, wantRetired: []string{"old.example", "rutracker.net"}},
		{name: "re-adding clears retired", stored: `{"rutracker":{"mirrors":[],"retired":["rutracker.net"]}}`, body: `{"mirrors":["RUTRACKER.NET"]}`,
			wantMirrors: []string{"RUTRACKER.NET"}, wantRetired: []string{}},
		{name: "domain swap keeps old main", stored: `{}`, body: `{"domain":"rutracker.net"}`,
			wantDomain: "rutracker.net", wantMirrors: []string{"rutracker.org"}},
		{name: "other field writes no list", stored: `{}`, body: `{"hour_limit":5}`},
		{name: "unconfirmed removed and retired", stored: withX, body: `{"mirrors":["rutracker.net"]}`,
			wantMirrors: []string{"rutracker.net"}, wantRetired: []string{"x.example"}, wantUnconfirmed: []string{}},
		{name: "active domain confirms", stored: withX, body: `{"active_domain":"X.example"}`,
			wantMirrors: []string{"rutracker.net", "x.example"}, wantUnconfirmed: []string{}, wantActive: "X.example"},
		{name: "main domain confirms", stored: withX, body: `{"domain":"x.example"}`,
			wantDomain: "x.example", wantMirrors: []string{"rutracker.net", "rutracker.org"}, wantUnconfirmed: []string{}},
		{name: "stale dialog keeps unseen mirror", stored: withX, body: `{"mirrors":["rutracker.net"],"mirrors_seen":["rutracker.net"]}`,
			wantMirrors: []string{"rutracker.net", "x.example"}, wantUnconfirmed: []string{"x.example"}},
		{name: "seen mirror removed", stored: withX, body: `{"mirrors":[],"mirrors_seen":["rutracker.net","x.example"]}`,
			wantMirrors: []string{}, wantRetired: []string{"rutracker.net", "x.example"}, wantUnconfirmed: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{settings: map[string]json.RawMessage{"trackers": json.RawMessage(tc.stored)}}
			var reloads int
			srv := newAddedServer(store, &reloads)
			if w := serveAdded(srv, http.MethodPut, "/api/trackers/rutracker", tc.body); w.Code != http.StatusNoContent {
				t.Fatalf("PUT %s = %d %s, want 204", tc.body, w.Code, w.Body.String())
			}
			item := storedOverlay(t, store)["rutracker"]
			if _, ok := item["mirrors_seen"]; ok {
				t.Errorf("mirrors_seen stored: %v", item)
			}
			if tc.wantDomain != "" && item["domain"] != tc.wantDomain {
				t.Errorf("domain = %v, want %s", item["domain"], tc.wantDomain)
			}
			if item["active_domain"] != tc.wantActive && tc.wantActive != nil {
				t.Errorf("active_domain = %#v, want %#v", item["active_domain"], tc.wantActive)
			}
			for _, f := range []struct {
				field string
				want  []string
			}{{"mirrors", tc.wantMirrors}, {"retired", tc.wantRetired}, {"unconfirmed", tc.wantUnconfirmed}} {
				got, ok := storedTrackerList(t, store, "rutracker", f.field)
				if f.want == nil {
					var before map[string]map[string]any
					_ = json.Unmarshal([]byte(tc.stored), &before)
					if _, was := before["rutracker"][f.field]; ok && !was {
						t.Errorf("%s written: %v, want no key", f.field, got)
					}
					continue
				}
				if !ok || !slices.Equal(got, f.want) {
					t.Errorf("%s = %v (present %v), want %v", f.field, got, ok, f.want)
				}
			}
		})
	}
}

func TestTrackerPatchListsReadOnly(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers":       json.RawMessage(`{"rutracker":{"mirrors":["rutracker.net","x.example"],"unconfirmed":["x.example"],"retired":["old.example"]}}`),
		"trackers_added": json.RawMessage(`["rutracker"]`),
	}}
	var reloads int
	srv := newAddedServer(store, &reloads)
	before := string(store.settings["trackers"])
	for _, tc := range []struct{ method, url, body, want string }{
		{http.MethodPut, "/api/trackers/rutracker", `{"retired":["a.example"]}`, "retired is read-only"},
		{http.MethodPut, "/api/trackers/rutracker", `{"retired":[]}`, "retired is read-only"},
		{http.MethodPut, "/api/trackers/rutracker", `{"unconfirmed":[]}`, "unconfirmed is read-only"},
		{http.MethodPut, "/api/trackers/rutracker", `{"mirrors":["x.example"],"unconfirmed":null}`, "unconfirmed is read-only"},
		{http.MethodPut, "/api/trackers/rutracker", `{"Retired":[]}`, "did you mean"},
		{http.MethodPut, "/api/trackers/rutracker", `{"mirrors":[],"mirrors_seen":"rutracker.net"}`, "mirrors_seen"},
		{http.MethodPut, "/api/trackers/rutracker", `{"mirrors":[],"mirrors_confirm":"x.example"}`, "mirrors_confirm"},
		{http.MethodPost, "/api/trackers/kinozal", `{"retired":["kinozal.tv"]}`, "retired is read-only"},
		{http.MethodPost, "/api/trackers/rutracker/test", `{"config":{"unconfirmed":["x.example"],"publicity":"public"}}`, "unconfirmed is read-only"},
	} {
		w := serveAdded(srv, tc.method, tc.url, tc.body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), tc.want) {
			t.Errorf("%s %s %s = %d %s, want 400 %q", tc.method, tc.url, tc.body, w.Code, w.Body.String(), tc.want)
		}
	}
	if string(store.settings["trackers"]) != before || reloads != 0 {
		t.Fatalf("rejected patch changed the store: %s, reloads %d", store.settings["trackers"], reloads)
	}
}

func TestPutTrackerDropsMirrorRetiredAfterDialogLoad(t *testing.T) {
	const stored = `{"rutracker":{"mirrors":["rutracker.net"],"retired":["C.example"]}}`
	for _, tc := range []struct {
		name, body  string
		wantCode    int
		wantRemoved []string
		wantMirrors []string
		wantRetired []string
		wantActive  any
		wantDomain  string
	}{
		{"seen then retired", `{"mirrors":["rutracker.net","c.example"],"mirrors_seen":["rutracker.net","c.example"]}`,
			http.StatusOK, []string{"C.example"}, []string{"rutracker.net"}, []string{"C.example"}, nil, ""},
		{"sent active retired", `{"active_domain":"c.example","mirrors":["rutracker.net","c.example"],"mirrors_seen":["rutracker.net","c.example"]}`,
			http.StatusOK, []string{"C.example"}, []string{"rutracker.net"}, []string{"C.example"}, "", ""},
		{"typed anew", `{"mirrors":["rutracker.net","c.example"],"mirrors_seen":["rutracker.net"]}`,
			http.StatusNoContent, nil, []string{"rutracker.net", "c.example"}, []string{}, nil, ""},
		{"no mirrors_seen", `{"mirrors":["rutracker.net","c.example"]}`,
			http.StatusNoContent, nil, []string{"rutracker.net", "c.example"}, []string{}, nil, ""},
		{"seen retired chosen as main", `{"domain":"c.example","mirrors":["rutracker.net","c.example"],"mirrors_seen":["rutracker.net","c.example"]}`,
			http.StatusNoContent, nil, []string{"rutracker.net", "rutracker.org"}, []string{}, nil, "c.example"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{settings: map[string]json.RawMessage{"trackers": json.RawMessage(stored)}}
			var reloads int
			srv := newAddedServer(store, &reloads)
			w := serveAdded(srv, http.MethodPut, "/api/trackers/rutracker", tc.body)
			if w.Code != tc.wantCode {
				t.Fatalf("PUT %s = %d %s, want %d", tc.body, w.Code, w.Body.String(), tc.wantCode)
			}
			if tc.wantRemoved != nil {
				var resp struct {
					RemovedMirrors []string `json:"removed_mirrors"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || !slices.Equal(resp.RemovedMirrors, tc.wantRemoved) {
					t.Errorf("body %s, want removed_mirrors %v", w.Body.String(), tc.wantRemoved)
				}
			} else if w.Body.Len() != 0 {
				t.Errorf("204 body = %q, want empty", w.Body.String())
			}
			if reloads != 1 {
				t.Errorf("reloads = %d, want 1", reloads)
			}
			if got, _ := storedTrackerList(t, store, "rutracker", "mirrors"); !slices.Equal(got, tc.wantMirrors) {
				t.Errorf("mirrors = %v, want %v", got, tc.wantMirrors)
			}
			if got, _ := storedTrackerList(t, store, "rutracker", "retired"); !slices.Equal(got, tc.wantRetired) {
				t.Errorf("retired = %v, want %v", got, tc.wantRetired)
			}
			if got := storedOverlay(t, store)["rutracker"]["domain"]; tc.wantDomain != "" && got != tc.wantDomain {
				t.Errorf("domain = %#v, want %s", got, tc.wantDomain)
			}
			if got := storedOverlay(t, store)["rutracker"]["active_domain"]; tc.wantActive != nil && got != tc.wantActive {
				t.Errorf("active_domain = %#v, want %#v", got, tc.wantActive)
			}
		})
	}
}

func TestPutTrackerConfirmsShownMirrors(t *testing.T) {
	const withX = `{"rutracker":{"mirrors":["rutracker.net","x.example"],"unconfirmed":["x.example"]}}`
	const withY = `{"rutracker":{"mirrors":["rutracker.net","x.example","y.example"],"unconfirmed":["x.example","y.example"]}}`
	for _, tc := range []struct {
		name, stored, paused, body string
		wantMirrors                []string
		wantUnconfirmed            []string
		wantRetired                []string
		wantPaused                 any
	}{
		{"kept shown mirror confirmed", withX, "x.example", `{"mirrors":["rutracker.net","x.example"],"mirrors_seen":["rutracker.net","x.example"],"mirrors_confirm":["x.example"]}`,
			[]string{"rutracker.net", "x.example"}, []string{}, nil, false},
		{"seen but not confirmed stays unconfirmed", withX, "x.example", `{"mirrors":["rutracker.net","x.example"],"mirrors_seen":["rutracker.net","x.example"]}`,
			[]string{"rutracker.net", "x.example"}, []string{"x.example"}, nil, true},
		{"removed shown mirror retired", withX, "x.example", `{"mirrors":["rutracker.net"],"mirrors_seen":["rutracker.net","x.example"]}`,
			[]string{"rutracker.net"}, []string{}, []string{"x.example"}, nil},
		{"no mirrors_seen keeps unconfirmed", withX, "x.example", `{"mirrors":["rutracker.net","x.example"]}`,
			[]string{"rutracker.net", "x.example"}, []string{"x.example"}, nil, true},
		{"job-added host stays unconfirmed", withY, "y.example", `{"mirrors":["rutracker.net","x.example"],"mirrors_seen":["rutracker.net","x.example"],"mirrors_confirm":["x.example"]}`,
			[]string{"rutracker.net", "x.example", "y.example"}, []string{"y.example"}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{settings: map[string]json.RawMessage{
				"trackers":    json.RawMessage(tc.stored),
				"mirror_sync": json.RawMessage(`{"paused":{"rutracker":{"host":"` + tc.paused + `","since":"2026-10-23T00:00:00Z"}}}`),
			}}
			var reloads int
			srv := newAddedServer(store, &reloads)
			if w := serveAdded(srv, http.MethodPut, "/api/trackers/rutracker", tc.body); w.Code != http.StatusNoContent {
				t.Fatalf("PUT %s = %d %s, want 204", tc.body, w.Code, w.Body.String())
			}
			if reloads != 1 {
				t.Errorf("reloads = %d, want 1", reloads)
			}
			if got, _ := storedTrackerList(t, store, "rutracker", "mirrors"); !slices.Equal(got, tc.wantMirrors) {
				t.Errorf("mirrors = %v, want %v", got, tc.wantMirrors)
			}
			if got, _ := storedTrackerList(t, store, "rutracker", "unconfirmed"); !slices.Equal(got, tc.wantUnconfirmed) {
				t.Errorf("unconfirmed = %v, want %v", got, tc.wantUnconfirmed)
			}
			if got, ok := storedTrackerList(t, store, "rutracker", "retired"); tc.wantRetired != nil && (!ok || !slices.Equal(got, tc.wantRetired)) {
				t.Errorf("retired = %v, want %v", got, tc.wantRetired)
			}
			if _, ok := storedMirrorSync(t, store).Paused["rutracker"]; tc.wantPaused != nil && ok != tc.wantPaused {
				t.Errorf("paused present = %v, want %v", ok, tc.wantPaused)
			}
		})
	}
}
