package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"ripperr/internal/settings"
)

type mirrorStateResp struct {
	Key         string                      `json:"key"`
	Password    string                      `json:"password"`
	Suggestions []settings.MirrorSuggestion `json:"suggestions"`
	Paused      string                      `json:"paused"`
	Auto        []settings.MirrorAuto       `json:"auto"`
	ConfirmOn   *bool                       `json:"confirm_on"`
}

func mirrorSyncDoc() json.RawMessage {
	recent := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	old := time.Now().Add(-40 * 24 * time.Hour).UTC().Format(time.RFC3339)
	return json.RawMessage(fmt.Sprintf(`{"last_run":%q,
"suggestions":{"kinozal":[{"kind":"remove","host":"kinozal.guru","reason":"dead"},{"kind":"main","host":"kinozal.me","from":"kinozal.tv","reason":"redirect"}]},
"paused":{"kinozal":{"host":"x.example","since":%q}},
"auto":{"kinozal":[{"kind":"added","host":"x.example","reason":"jackett","at":%q},{"kind":"removed","host":"old.example","reason":"dead","at":%q}]}}`,
		recent, recent, recent, old))
}

func mirrorStateStore(confirmOn bool, doc json.RawMessage) *fakeStore {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers_added": json.RawMessage(`["kinozal","rutor"]`),
		"trackers":       json.RawMessage(`{"kinozal":{"mirrors":["kinozal.me","x.example"],"unconfirmed":["x.example"],"login":"u","password":"SEC-kp"}}`),
		"schedule":       json.RawMessage(fmt.Sprintf(`{"mirror_confirm":%t}`, confirmOn)),
	}}
	if doc != nil {
		store.settings["mirror_sync"] = doc
	}
	return store
}

func getMirrorState(t *testing.T, srv *Server) map[string]mirrorStateResp {
	t.Helper()
	w := serveAdded(srv, http.MethodGet, "/api/trackers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/trackers status = %d, body: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "SEC-kp") {
		t.Fatalf("password leaked: %s", w.Body.String())
	}
	var list []mirrorStateResp
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	out := make(map[string]mirrorStateResp, len(list))
	for _, it := range list {
		out[it.Key] = it
	}
	return out
}

func suggestionIDs(list []settings.MirrorSuggestion) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.Kind+"|"+s.Host)
	}
	return out
}

func TestGetTrackersMirrorState(t *testing.T) {
	var reloads int
	srv := newAddedServer(mirrorStateStore(true, mirrorSyncDoc()), &reloads)
	got := getMirrorState(t, srv)
	kz := got["kinozal"]
	if want := []string{"confirm|x.example", "main|kinozal.me"}; !slices.Equal(suggestionIDs(kz.Suggestions), want) {
		t.Errorf("kinozal suggestions = %v, want %v", suggestionIDs(kz.Suggestions), want)
	}
	if kz.Paused != "x.example" {
		t.Errorf("kinozal paused = %q, want x.example", kz.Paused)
	}
	if len(kz.Auto) != 1 || kz.Auto[0].Host != "x.example" {
		t.Errorf("kinozal auto = %+v, want one x.example entry", kz.Auto)
	}
	if kz.ConfirmOn == nil || !*kz.ConfirmOn {
		t.Errorf("kinozal confirm_on = %v, want true", kz.ConfirmOn)
	}
	if kz.Password != settings.SecretMask {
		t.Errorf("kinozal password = %q, want mask", kz.Password)
	}
	rt := got["rutor"]
	if len(rt.Suggestions) != 0 || rt.Paused != "" || len(rt.Auto) != 0 || rt.ConfirmOn == nil || !*rt.ConfirmOn {
		t.Errorf("rutor = %+v, want no mirror state and confirm_on true", rt)
	}

	w := serveAdded(srv, http.MethodGet, "/api/trackers/types", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/trackers/types status = %d", w.Code)
	}
	for _, field := range []string{`"suggestions"`, `"paused"`, `"auto"`, `"confirm_on"`} {
		if strings.Contains(w.Body.String(), field) {
			t.Errorf("/types carries %s", field)
		}
	}

	off := getMirrorState(t, newAddedServer(mirrorStateStore(false, mirrorSyncDoc()), &reloads))["kinozal"]
	if off.ConfirmOn == nil || *off.ConfirmOn {
		t.Errorf("toggle off: confirm_on = %v, want false", off.ConfirmOn)
	}
	if off.Paused != "" {
		t.Errorf("toggle off: paused = %q, want hidden", off.Paused)
	}
	if want := []string{"main|kinozal.me"}; !slices.Equal(suggestionIDs(off.Suggestions), want) {
		t.Errorf("toggle off: suggestions = %v, want %v", suggestionIDs(off.Suggestions), want)
	}

	for name, doc := range map[string]json.RawMessage{"absent": nil, "invalid": json.RawMessage(`{"suggestions":`)} {
		plain := getMirrorState(t, newAddedServer(mirrorStateStore(true, doc), &reloads))["kinozal"]
		if len(plain.Suggestions) != 1 || plain.Suggestions[0].Kind != settings.MirrorKindConfirm || plain.Paused != "" || len(plain.Auto) != 0 {
			t.Errorf("%s doc: kinozal = %+v, want only the overlay confirm item", name, plain)
		}
	}
	if reloads != 0 {
		t.Errorf("reloads = %d, want 0", reloads)
	}
}

func storedMirrorSync(t *testing.T, store *fakeStore) settings.MirrorSync {
	t.Helper()
	var d settings.MirrorSync
	if err := json.Unmarshal(store.settings["mirror_sync"], &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDismissSuggestion(t *testing.T) {
	var reloads int
	store := mirrorStateStore(true, mirrorSyncDoc())
	srv := newAddedServer(store, &reloads)
	trackersBefore := slices.Clone(store.settings["trackers"])
	const url = "/api/trackers/kinozal/suggestions/dismiss"

	w := serveAdded(srv, http.MethodPost, url, `{"kind":"main","host":"kinozal.me"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("dismiss status = %d, body: %s", w.Code, w.Body.String())
	}
	d := storedMirrorSync(t, store)
	if !slices.Equal(d.Dismissed, []string{"kinozal|main|kinozal.me"}) {
		t.Errorf("dismissed = %v", d.Dismissed)
	}
	if want := []string{"remove|kinozal.guru"}; !slices.Equal(suggestionIDs(d.Suggestions["kinozal"]), want) {
		t.Errorf("stored suggestions = %v, want %v", suggestionIDs(d.Suggestions["kinozal"]), want)
	}
	if !bytes.Equal(store.settings["trackers"], trackersBefore) {
		t.Errorf("trackers doc changed: %s", store.settings["trackers"])
	}
	if reloads != 0 {
		t.Errorf("reloads = %d, want 0", reloads)
	}

	if w = serveAdded(srv, http.MethodPost, url, `{"kind":"main","host":"kinozal.me"}`); w.Code != http.StatusNotFound {
		t.Errorf("repeat dismiss status = %d, want 404", w.Code)
	}
	for _, body := range []string{
		`{"kind":"confirm","host":"x.example"}`,
		`{"kind":"added","host":"x.example"}`,
		`{"kind":"","host":"kinozal.guru"}`,
		`{"kind":"remove","host":"bad host"}`,
		`{"kind":"remove","host":"1.2.3.4"}`,
		`{"kind":"remove","host":""}`,
		`{"kind":"remove","host":"kinozal.guru","x":1}`,
		`{"kind":"remove","host":"kinozal.guru"} {}`,
		`{"kind":`,
		``,
	} {
		if w = serveAdded(srv, http.MethodPost, url, body); w.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, w.Code)
		}
	}
	for _, key := range []string{"rutracker", "nope"} {
		if w = serveAdded(srv, http.MethodPost, "/api/trackers/"+key+"/suggestions/dismiss", `{"kind":"remove","host":"kinozal.guru"}`); w.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", key, w.Code)
		}
	}
	if d = storedMirrorSync(t, store); len(d.Dismissed) != 1 || len(d.Suggestions["kinozal"]) != 1 {
		t.Errorf("rejected requests changed the doc: %+v", d)
	}
	if reloads != 0 {
		t.Errorf("reloads = %d, want 0", reloads)
	}
}

func rawTrackerDoc(t *testing.T, store *fakeStore) map[string]map[string]json.RawMessage {
	t.Helper()
	var doc map[string]map[string]json.RawMessage
	if err := json.Unmarshal(store.settings["trackers"], &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestConfirmMirror(t *testing.T) {
	var reloads int
	store := mirrorStateStore(true, mirrorSyncDoc())
	store.settings["trackers"] = json.RawMessage(`{"kinozal":{"mirrors":["kinozal.me","x.example"],"unconfirmed":["x.example","gone.example"],"login":"u","password":"SEC-kp&<"},"rutor":{"mirrors":["rutor.is"],"cookies":"SEC-rc"}}`)
	srv := newAddedServer(store, &reloads)
	before := rawTrackerDoc(t, store)
	const url = "/api/trackers/kinozal/mirrors/confirm"

	for _, body := range []string{`{"host":"x.example","kind":"confirm"}`, `{"host":`, ``, `{"host":"bad host"}`, `{"host":""}`} {
		if w := serveAdded(srv, http.MethodPost, url, body); w.Code != http.StatusBadRequest {
			t.Errorf("body %s: status = %d, want 400", body, w.Code)
		}
	}
	for _, host := range []string{"kinozal.me", "gone.example", "y.example"} {
		if w := serveAdded(srv, http.MethodPost, url, `{"host":"`+host+`"}`); w.Code != http.StatusNotFound {
			t.Errorf("host %s: status = %d, want 404", host, w.Code)
		}
	}
	if w := serveAdded(srv, http.MethodPost, "/api/trackers/rutracker/mirrors/confirm", `{"host":"x.example"}`); w.Code != http.StatusNotFound {
		t.Errorf("not added: status = %d, want 404", w.Code)
	}
	if reloads != 0 {
		t.Fatalf("rejected requests reloaded %d times", reloads)
	}

	w := serveAdded(srv, http.MethodPost, url, `{"host":"X.example"}`)
	if w.Code != http.StatusNoContent {
		t.Fatalf("confirm status = %d, body: %s", w.Code, w.Body.String())
	}
	if reloads != 1 {
		t.Errorf("reloads = %d, want 1", reloads)
	}
	after := rawTrackerDoc(t, store)
	if got := string(after["kinozal"]["unconfirmed"]); got != `["gone.example"]` {
		t.Errorf("unconfirmed = %s, want [\"gone.example\"]", got)
	}
	for field, raw := range before["kinozal"] {
		if field != "unconfirmed" && !bytes.Equal(after["kinozal"][field], raw) {
			t.Errorf("kinozal.%s changed: %s -> %s", field, raw, after["kinozal"][field])
		}
	}
	if len(after["kinozal"]) != len(before["kinozal"]) || len(after["rutor"]) != len(before["rutor"]) {
		t.Errorf("entry fields changed: %v", after)
	}
	for field, raw := range before["rutor"] {
		if !bytes.Equal(after["rutor"][field], raw) {
			t.Errorf("rutor.%s changed", field)
		}
	}
	if _, ok := storedMirrorSync(t, store).Paused["kinozal"]; ok {
		t.Error("paused not cleared")
	}

	store.settings["trackers"] = json.RawMessage(`{"kinozal":{"mirrors":["kinozal.me","x.example"],"unconfirmed":["x.example"]}}`)
	if w = serveAdded(srv, http.MethodPost, url, `{"host":"x.example"}`); w.Code != http.StatusNoContent {
		t.Fatalf("second confirm status = %d", w.Code)
	}
	if got := string(rawTrackerDoc(t, store)["kinozal"]["unconfirmed"]); got != `[]` {
		t.Errorf("unconfirmed = %s, want []", got)
	}
}

func TestTrackersLockShared(t *testing.T) {
	var reloads int
	store := &fakeStore{settings: map[string]json.RawMessage{"trackers_added": json.RawMessage(`["kinozal"]`)}}
	srv := newAddedServer(store, &reloads)
	unlock := settings.LockTrackers()
	done := make(chan int, 1)
	go func() {
		done <- serveAdded(srv, http.MethodPut, "/api/trackers/kinozal", `{"enabled":true}`).Code
	}()
	select {
	case code := <-done:
		unlock()
		t.Fatalf("PUT finished with %d while the trackers lock was held", code)
	case <-time.After(200 * time.Millisecond):
	}
	unlock()
	select {
	case code := <-done:
		if code != http.StatusNoContent {
			t.Errorf("PUT status = %d, want 204", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PUT still blocked after unlock")
	}
}
