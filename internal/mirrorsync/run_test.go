package mirrorsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/model"
	"ripperr/internal/notify"
	"ripperr/internal/settings"
	"ripperr/internal/system/logger"
)

const kinozalPath = "Definitions/kinozal.yml"

type memStore struct {
	mu      sync.Mutex
	data    map[string]json.RawMessage
	saves   int
	saveErr map[string]error
}

func newMemStore(docs map[string]string) *memStore {
	s := &memStore{data: map[string]json.RawMessage{}, saveErr: map[string]error{}}
	for k, v := range docs {
		s.data[k] = json.RawMessage(v)
	}
	return s
}

func (s *memStore) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[key]
	return append(json.RawMessage(nil), v...), ok, nil
}

func (s *memStore) SaveSetting(_ context.Context, key string, value json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.saveErr[key]; err != nil {
		return err
	}
	s.saves++
	s.data[key] = append(json.RawMessage(nil), value...)
	return nil
}

func (s *memStore) raw(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.data[key])
}

func (s *memStore) set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = json.RawMessage(value)
}

func (s *memStore) saveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saves
}

func (s *memStore) entry(t *testing.T, key string) map[string]json.RawMessage {
	t.Helper()
	var doc map[string]map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s.raw("trackers")), &doc); err != nil {
		t.Fatalf("trackers doc: %v", err)
	}
	return doc[key]
}

func (s *memStore) list(t *testing.T, key, field string) []string {
	t.Helper()
	raw, ok := s.entry(t, key)[field]
	if !ok {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s.%s: %v", key, field, err)
	}
	return out
}

func (s *memStore) doc(t *testing.T) settings.MirrorSync {
	t.Helper()
	d, err := settings.GetMirrorSync(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func linksYAML(hosts ...string) string {
	var b strings.Builder
	b.WriteString("id: kinozal\nlinks:\n")
	for _, h := range hosts {
		b.WriteString("  - https://" + h + "/\n")
	}
	return b.String()
}

func jackettServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("credentials in jackett request: %v", r.Header)
		}
		if r.URL.Path != "/"+jackettRoot+kinozalPath {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func logCount(level, needle string) int {
	n := 0
	for _, e := range logger.Snapshot(0) {
		if e.Level == level && strings.Contains(e.Message, needle) {
			n++
		}
	}
	return n
}

type env struct {
	store      *memStore
	deps       Deps
	now        time.Time
	up         map[string]bool
	idle       []bool
	idleCalls  int
	lastIdle   bool
	waits      []time.Duration
	reloads    int
	reloadDoc  settings.MirrorSync
	reloadIdle bool
}

func newEnv(t *testing.T, docs map[string]string, item config.TrackerItem, yml string, up ...string) *env {
	t.Helper()
	if _, ok := docs["trackers_added"]; !ok {
		docs["trackers_added"] = `["kinozal"]`
	}
	srv := jackettServer(t, http.StatusOK, yml)
	e := &env{store: newMemStore(docs), now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), up: map[string]bool{}}
	for _, h := range up {
		e.up[h] = true
	}
	e.deps = Deps{
		Store:   e.store,
		Items:   config.Tracker{"kinozal": &item},
		HTTP:    srv.Client(),
		Sources: []string{srv.URL + "/{path}"},
		Check: func(_ context.Context, host string, _ int) (bool, string) {
			return e.up[settings.MirrorHostKey(host)], ""
		},
		ConfirmOn: true,
		Reload: func() {
			e.reloads++
			e.reloadDoc = e.store.doc(t)
			e.reloadIdle = e.lastIdle
		},
		Idle: func() bool {
			v := true
			if len(e.idle) > 0 {
				v, e.idle = e.idle[0], e.idle[1:]
			}
			e.idleCalls++
			e.lastIdle = v
			return v
		},
		Entries: func(context.Context) ([]string, error) {
			return []string{"https://kinozal.me/details.php?id=1"}, nil
		},
		StoredURLs: func(context.Context) ([]string, error) { return nil, nil },
		Now:        func() time.Time { return e.now },
		After: func(d time.Duration) <-chan time.Time {
			e.waits = append(e.waits, d)
			ch := make(chan time.Time, 1)
			ch <- e.now
			return ch
		},
		StartDelay: 2 * time.Minute,
		Interval:   24 * time.Hour,
	}
	return e
}

func kinozalItem(mirrors ...string) config.TrackerItem {
	return config.TrackerItem{Name: "kinozal", Domain: "kinozal.tv", Mirrors: mirrors}
}

func checkList(t *testing.T, name string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func TestRunOnceAppendsAndReloads(t *testing.T) {
	overlay := `{"kinozal":{"credentials":"u:p&<x>","password":"p&<x>\"q","cookies":"a=b; c=d","mirrors":["kinozal.me"]}}`
	e := newEnv(t, map[string]string{"trackers": overlay}, kinozalItem("kinozal.me"),
		linksYAML("kinozal.tv", "kinozal.me", "kinozal.guru"), "kinozal.tv", "kinozal.me", "kinozal.guru")
	e.idle = []bool{false, true}

	runOnce(context.Background(), e.deps)

	checkList(t, "mirrors", e.store.list(t, "kinozal", "mirrors"), "kinozal.me", "kinozal.guru")
	checkList(t, "unconfirmed", e.store.list(t, "kinozal", "unconfirmed"), "kinozal.guru")
	entry := e.store.entry(t, "kinozal")
	for field, want := range map[string]string{"credentials": `"u:p&<x>"`, "password": `"p&<x>\"q"`, "cookies": `"a=b; c=d"`} {
		if string(entry[field]) != want {
			t.Errorf("%s = %s, want %s", field, entry[field], want)
		}
	}
	doc := e.store.doc(t)
	wantAuto := []settings.MirrorAuto{{Kind: settings.MirrorAutoAdded, Host: "kinozal.guru", Reason: settings.MirrorReasonJackett, At: e.now}}
	if !slices.Equal(doc.Auto["kinozal"], wantAuto) {
		t.Errorf("auto = %+v, want %+v", doc.Auto["kinozal"], wantAuto)
	}
	if src := doc.Sources[kinozalPath]; len(src.Links) != 3 || !src.Fetched.Equal(e.now) {
		t.Errorf("source cache = %+v", src)
	}
	if e.reloads != 1 {
		t.Fatalf("reloads = %d, want 1", e.reloads)
	}
	if !e.reloadDoc.LastRun.Equal(e.now) {
		t.Errorf("last_run at reload = %v, want %v", e.reloadDoc.LastRun, e.now)
	}
	if !e.reloadIdle || e.idleCalls != 2 {
		t.Errorf("reload while busy: idle %v after %d calls", e.reloadIdle, e.idleCalls)
	}
	checkList(t, "waits", durations(e.waits), "30s")
}

func durations(ds []time.Duration) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.String()
	}
	return out
}

func TestRunOnceNoChangeNoReload(t *testing.T) {
	overlay := `{"kinozal":{"password":"secret","mirrors":["kinozal.me"]}}`
	e := newEnv(t, map[string]string{"trackers": overlay}, kinozalItem("kinozal.me"),
		linksYAML("kinozal.tv", "kinozal.me"), "kinozal.tv", "kinozal.me")

	runOnce(context.Background(), e.deps)

	if got := e.store.raw("trackers"); got != overlay {
		t.Errorf("trackers doc changed: %s", got)
	}
	if e.reloads != 0 || e.idleCalls != 0 {
		t.Errorf("reloads = %d, idle calls = %d, want 0", e.reloads, e.idleCalls)
	}
	if doc := e.store.doc(t); !doc.LastRun.Equal(e.now) {
		t.Errorf("last_run = %v, want %v", doc.LastRun, e.now)
	}
}

func TestRunOnceAutoRetire(t *testing.T) {
	e := newEnv(t, map[string]string{
		"trackers":    `{"kinozal":{"mirrors":["kinozal.me","kinozal.cc"]}}`,
		"mirror_sync": `{"fails":{"kinozal":{"kinozal.cc":2}}}`,
	}, kinozalItem("kinozal.me", "kinozal.cc"), linksYAML("kinozal.tv", "kinozal.me"), "kinozal.tv", "kinozal.me")
	e.deps.ConfirmOn = false

	runOnce(context.Background(), e.deps)

	checkList(t, "mirrors", e.store.list(t, "kinozal", "mirrors"), "kinozal.me")
	checkList(t, "retired", e.store.list(t, "kinozal", "retired"), "kinozal.cc")
	doc := e.store.doc(t)
	wantAuto := []settings.MirrorAuto{{Kind: settings.MirrorAutoRemoved, Host: "kinozal.cc", Reason: settings.MirrorReasonDead, At: e.now}}
	if !slices.Equal(doc.Auto["kinozal"], wantAuto) {
		t.Errorf("auto = %+v, want %+v", doc.Auto["kinozal"], wantAuto)
	}
	if len(doc.Suggestions) != 0 {
		t.Errorf("suggestions = %+v, want none", doc.Suggestions)
	}
	if e.reloads != 1 {
		t.Errorf("reloads = %d, want 1", e.reloads)
	}
}

func TestRunOnceFetchFailureUsesCache(t *testing.T) {
	e := newEnv(t, map[string]string{
		"trackers":    `{"kinozal":{"mirrors":["kinozal.me"]}}`,
		"mirror_sync": `{"sources":{"Definitions/kinozal.yml":{"links":["kinozal.tv","kinozal.me","kinozal.guru"],"legacy":[],"fetched":"2026-10-01T00:00:00Z"}}}`,
	}, kinozalItem("kinozal.me"), "", "kinozal.tv", "kinozal.me", "kinozal.guru")
	bad := jackettServer(t, http.StatusInternalServerError, "oops")
	e.deps.HTTP, e.deps.Sources = bad.Client(), []string{bad.URL + "/{path}"}
	const warn = "mirror sync: jackett unavailable for 1 of 1 sources, cached lists used: Definitions/kinozal.yml: status 500"
	before := logCount("WARN", "mirror sync: jackett unavailable")
	exact := logCount("WARN", warn)

	runOnce(context.Background(), e.deps)

	if got := logCount("WARN", "mirror sync: jackett unavailable") - before; got != 1 {
		t.Errorf("jackett warnings = %d, want 1", got)
	}
	if got := logCount("WARN", warn) - exact; got != 1 {
		t.Errorf("no warning %q", warn)
	}
	checkList(t, "mirrors", e.store.list(t, "kinozal", "mirrors"), "kinozal.me", "kinozal.guru")
	doc := e.store.doc(t)
	if src := doc.Sources[kinozalPath]; !src.Fetched.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("cache overwritten: %+v", src)
	}
	if !doc.LastRun.Equal(e.now) {
		t.Errorf("last_run = %v, want %v", doc.LastRun, e.now)
	}
}

func TestRunOnceCancelledSavesNothing(t *testing.T) {
	e := newEnv(t, map[string]string{"trackers": `{"kinozal":{"mirrors":["kinozal.me"]}}`}, kinozalItem("kinozal.me"),
		linksYAML("kinozal.tv", "kinozal.me", "kinozal.guru"), "kinozal.tv", "kinozal.me", "kinozal.guru")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	check := e.deps.Check
	probes := 0
	e.deps.Check = func(ctx context.Context, host string, pt int) (bool, string) {
		cancel()
		probes++
		return check(ctx, host, pt)
	}

	runOnce(ctx, e.deps)

	if probes == 0 {
		t.Error("run stopped before probes, cancel path not reached")
	}
	if n := e.store.saveCount(); n != 0 {
		t.Errorf("saves = %d, want 0", n)
	}
	if e.reloads != 0 {
		t.Errorf("reloads = %d, want 0", e.reloads)
	}
}

func runWaits(t *testing.T, st *memStore, now time.Time, fire, want int) []time.Duration {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan time.Duration, 10)
	n := 0
	d := Deps{
		Store:      st,
		Items:      config.Tracker{},
		HTTP:       http.DefaultClient,
		Sources:    []string{"http://127.0.0.1:9/{path}"},
		Check:      func(context.Context, string, int) (bool, string) { return false, "" },
		Reload:     func() {},
		Idle:       func() bool { return true },
		Now:        func() time.Time { return now },
		StartDelay: 2 * time.Minute,
		Interval:   24 * time.Hour,
		After: func(w time.Duration) <-chan time.Time {
			n++
			calls <- w
			ch := make(chan time.Time, 1)
			if n <= fire {
				ch <- now
			}
			return ch
		},
	}
	done := make(chan struct{})
	go func() {
		Run(ctx, d)
		close(done)
	}()
	var got []time.Duration
	for len(got) < want {
		select {
		case w := <-calls:
			got = append(got, w)
		case <-time.After(5 * time.Second):
			t.Fatalf("waits = %v, want %d", got, want)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	return got
}

func TestRunWaits(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	lastRun := func(ago time.Duration) map[string]string {
		return map[string]string{"mirror_sync": fmt.Sprintf(`{"last_run":%q}`, now.Add(-ago).Format(time.RFC3339))}
	}
	for _, tc := range []struct {
		name string
		docs map[string]string
		want time.Duration
	}{
		{"last run 1h ago", lastRun(time.Hour), 23 * time.Hour},
		{"never ran", map[string]string{}, 2 * time.Minute},
		{"last run 25h ago", lastRun(25 * time.Hour), 2 * time.Minute},
		{"last run 23h59m ago", lastRun(23*time.Hour + 59*time.Minute), 2 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := runWaits(t, newMemStore(tc.docs), now, 0, 1); got[0] != tc.want {
				t.Errorf("first wait = %v, want %v", got[0], tc.want)
			}
		})
	}
	t.Run("save error", func(t *testing.T) {
		st := newMemStore(map[string]string{})
		st.saveErr["mirror_sync"] = errors.New("disk full")
		got := runWaits(t, st, now, 1, 2)
		if got[0] != 2*time.Minute || got[1] < 24*time.Hour {
			t.Errorf("waits = %v, want [2m >=24h]", got)
		}
	})
}

func TestRunOnceKeepsDismissed(t *testing.T) {
	for _, confirmOn := range []bool{true, false} {
		t.Run(fmt.Sprintf("confirm %v", confirmOn), func(t *testing.T) {
			overlay := `{"kinozal":{"mirrors":["kinozal.me","kinozal.cc"]}}`
			e := newEnv(t, map[string]string{
				"trackers":    overlay,
				"mirror_sync": `{"fails":{"kinozal":{"kinozal.cc":2}},"dismissed":["kinozal|remove|kinozal.cc"]}`,
			}, kinozalItem("kinozal.me", "kinozal.cc"), linksYAML("kinozal.tv", "kinozal.me"), "kinozal.tv", "kinozal.me")
			e.deps.ConfirmOn = confirmOn

			runOnce(context.Background(), e.deps)

			if got := e.store.raw("trackers"); got != overlay {
				t.Errorf("trackers doc changed: %s", got)
			}
			doc := e.store.doc(t)
			if len(doc.Suggestions) != 0 {
				t.Errorf("suggestions = %+v, want none", doc.Suggestions)
			}
			checkList(t, "dismissed", doc.Dismissed, "kinozal|remove|kinozal.cc")
			if doc.Fails["kinozal"]["kinozal.cc"] != 3 || !doc.LastRun.Equal(e.now) {
				t.Errorf("run not saved: fails %v, last_run %v", doc.Fails, doc.LastRun)
			}
			if e.reloads != 0 {
				t.Errorf("reloads = %d, want 0", e.reloads)
			}
		})
	}
}

func TestRunOnceRespectsConcurrentEdit(t *testing.T) {
	e := newEnv(t, map[string]string{"trackers": `{"kinozal":{"password":"secret","mirrors":["kinozal.me"]}}`}, kinozalItem("kinozal.me"),
		linksYAML("kinozal.tv", "kinozal.me", "kinozal.guru"), "kinozal.tv", "kinozal.me", "kinozal.guru")
	check := e.deps.Check
	e.deps.Check = func(ctx context.Context, host string, pt int) (bool, string) {
		if host == "kinozal.guru" {
			e.store.set("trackers", `{"kinozal":{"password":"secret","mirrors":["kinozal.me","kinozal.club"]}}`)
		}
		return check(ctx, host, pt)
	}

	runOnce(context.Background(), e.deps)

	checkList(t, "mirrors", e.store.list(t, "kinozal", "mirrors"), "kinozal.me", "kinozal.club", "kinozal.guru")
	if got := string(e.store.entry(t, "kinozal")["password"]); got != `"secret"` {
		t.Errorf("password = %s", got)
	}
}

func prunedEnv(t *testing.T) *env {
	item := config.TrackerItem{Name: "kinozal", Domain: "kinozal.me", Mirrors: []string{"kinozal.guru"}, Retired: []string{"kinozal.tv"}}
	return newEnv(t, map[string]string{"trackers": `{"kinozal":{"domain":"kinozal.me","mirrors":["kinozal.guru"],"retired":["kinozal.tv"]}}`},
		item, linksYAML("kinozal.me", "kinozal.guru", "kinozal.tv"), "kinozal.me", "kinozal.guru", "kinozal.tv")
}

func TestRunOncePrunesUnreferenced(t *testing.T) {
	e := prunedEnv(t)
	const info = "mirror sync: kinozal: pruned retired kinozal.tv (no torrent references it)"
	before := logCount("INFO", info)

	runOnce(context.Background(), e.deps)

	checkList(t, "retired", e.store.list(t, "kinozal", "retired"))
	if _, ok := e.store.entry(t, "kinozal")["retired"]; !ok {
		t.Error("retired key not rewritten")
	}
	checkList(t, "mirrors", e.store.list(t, "kinozal", "mirrors"), "kinozal.guru")
	doc := e.store.doc(t)
	want := []settings.MirrorPruned{{Host: "kinozal.tv", At: e.now}}
	if !slices.Equal(doc.Pruned["kinozal"], want) {
		t.Errorf("pruned = %+v, want %+v", doc.Pruned["kinozal"], want)
	}
	if got := logCount("INFO", info) - before; got != 1 {
		t.Errorf("prune info lines = %d, want 1", got)
	}
	if e.reloads != 1 {
		t.Errorf("reloads = %d, want 1", e.reloads)
	}

	// после перезагрузки cfg уже без retired, вернуть хост не дает только запись pruned
	e.deps.Items = config.Tracker{"kinozal": {Name: "kinozal", Domain: "kinozal.me", Mirrors: []string{"kinozal.guru"}}}
	e.now = e.now.Add(25 * time.Hour)
	runOnce(context.Background(), e.deps)
	checkList(t, "mirrors after second run", e.store.list(t, "kinozal", "mirrors"), "kinozal.guru")
	if e.reloads != 1 {
		t.Errorf("reloads after second run = %d, want 1", e.reloads)
	}
}

func checkKept(t *testing.T, e *env) {
	t.Helper()
	checkList(t, "retired", e.store.list(t, "kinozal", "retired"), "kinozal.tv")
	doc := e.store.doc(t)
	if len(doc.Pruned) != 0 {
		t.Errorf("pruned = %+v, want none", doc.Pruned)
	}
	if !doc.LastRun.Equal(e.now) {
		t.Errorf("last_run = %v, want %v", doc.LastRun, e.now)
	}
	if e.reloads != 0 {
		t.Errorf("reloads = %d, want 0", e.reloads)
	}
}

func TestRunOnceKeepsReferenced(t *testing.T) {
	t.Run("live list", func(t *testing.T) {
		e := prunedEnv(t)
		e.deps.Entries = func(context.Context) ([]string, error) {
			return []string{"https://kinozal.me/details.php?id=1", "https://KINOZAL.TV/details.php?id=2"}, nil
		}
		runOnce(context.Background(), e.deps)
		checkKept(t, e)
	})
	t.Run("history only", func(t *testing.T) {
		e := prunedEnv(t)
		e.deps.StoredURLs = func(context.Context) ([]string, error) {
			return []string{"https://kinozal.tv/details.php?id=3"}, nil
		}
		runOnce(context.Background(), e.deps)
		checkKept(t, e)
	})
}

func checkPruneSkipped(t *testing.T, e *env, reason string) {
	t.Helper()
	before := logCount("WARN", "mirror sync: retired prune skipped")
	exact := logCount("WARN", "mirror sync: retired prune skipped: "+reason)
	runOnce(context.Background(), e.deps)
	if got := logCount("WARN", "mirror sync: retired prune skipped") - before; got != 1 {
		t.Errorf("prune warnings = %d, want 1", got)
	}
	if got := logCount("WARN", "mirror sync: retired prune skipped: "+reason) - exact; got != 1 {
		t.Errorf("no warning with reason %q", reason)
	}
	checkKept(t, e)
}

func TestRunOnceKeepsOnPartialList(t *testing.T) {
	e := prunedEnv(t)
	e.deps.Entries = func(context.Context) ([]string, error) {
		return []string{"https://kinozal.me/details.php?id=1"}, fmt.Errorf("%w: 1 skipped", model.ErrPartialList)
	}
	checkPruneSkipped(t, e, "partial list")
}

func TestRunOnceSkipsPruneOnBadList(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		listErr error
		histErr error
		reason  string
	}{
		{"list error", []string{"https://kinozal.me/details.php?id=1"}, errors.New("connection refused"), nil, "list error"},
		{"empty list", []string{}, nil, nil, "empty list"},
		{"no urls", []string{"", "  "}, nil, nil, "no URLs"},
		{"history error", []string{"https://kinozal.me/details.php?id=1"}, nil, errors.New("database is locked"), "history error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := prunedEnv(t)
			e.deps.Entries = func(context.Context) ([]string, error) { return tc.entries, tc.listErr }
			e.deps.StoredURLs = func(context.Context) ([]string, error) { return nil, tc.histErr }
			checkPruneSkipped(t, e, tc.reason)
		})
	}
}

func notifyEnv(t *testing.T, docs map[string]string, item config.TrackerItem, yml string, up ...string) (*env, *[]notify.Event) {
	t.Helper()
	e := newEnv(t, docs, item, yml, up...)
	var sent []notify.Event
	e.deps.Notify = func(ev notify.Event) { sent = append(sent, ev) }
	return e, &sent
}

func redirectEnv(t *testing.T, confirmOn bool) (*env, *[]notify.Event) {
	e, sent := notifyEnv(t, map[string]string{"trackers": `{"kinozal":{"mirrors":["kinozal.me"]}}`}, kinozalItem("kinozal.me"),
		linksYAML("kinozal.tv", "kinozal.me"), "kinozal.me")
	e.deps.ConfirmOn = confirmOn
	e.deps.Check = func(_ context.Context, host string, _ int) (bool, string) {
		if settings.MirrorHostKey(host) == "kinozal.tv" {
			return false, "kinozal.me"
		}
		return true, ""
	}
	return e, sent
}

func TestRunOnceNotifiesNewOnly(t *testing.T) {
	const mainLine = "kinozal: основной адрес kinozal.me вместо kinozal.tv"
	check := func(t *testing.T, sent *[]notify.Event, want ...string) {
		t.Helper()
		if len(*sent) != len(want) {
			t.Fatalf("digests = %d (%+v), want %d", len(*sent), *sent, len(want))
		}
		for i, ev := range *sent {
			if ev.Kind != notify.KindMirrorSuggested || ev.Text != "Зеркала трекеров: "+want[i] {
				t.Errorf("digest %d = %+v, want text %q", i, ev, want[i])
			}
		}
	}
	for _, confirmOn := range []bool{true, false} {
		t.Run(fmt.Sprintf("main suggestion once, confirm %v", confirmOn), func(t *testing.T) {
			e, sent := redirectEnv(t, confirmOn)
			runOnce(context.Background(), e.deps)
			check(t, sent, mainLine)
			runOnce(context.Background(), e.deps)
			check(t, sent, mainLine)
		})
	}
	t.Run("legacy domain digest has no remove of the domain", func(t *testing.T) {
		yml := linksYAML("kinozal.me") + "legacylinks:\n  - https://kinozal.tv/\n"
		e, sent := notifyEnv(t, map[string]string{"trackers": `{"kinozal":{"mirrors":["kinozal.me"]}}`}, kinozalItem("kinozal.me"),
			yml, "kinozal.tv", "kinozal.me")
		runOnce(context.Background(), e.deps)
		if got := len(e.store.doc(t).Suggestions["kinozal"]); got != 2 {
			t.Fatalf("stored suggestions = %d, want main and remove", got)
		}
		check(t, sent, mainLine)
	})
	t.Run("auto mode automatic add sends nothing", func(t *testing.T) {
		e, sent := notifyEnv(t, map[string]string{"trackers": `{"kinozal":{"mirrors":["kinozal.me"]}}`}, kinozalItem("kinozal.me"),
			linksYAML("kinozal.tv", "kinozal.me", "kinozal.guru"), "kinozal.tv", "kinozal.me", "kinozal.guru")
		e.deps.ConfirmOn = false
		runOnce(context.Background(), e.deps)
		checkList(t, "mirrors", e.store.list(t, "kinozal", "mirrors"), "kinozal.me", "kinozal.guru")
		check(t, sent)
	})
	t.Run("auto mode automatic removal sends nothing", func(t *testing.T) {
		e, sent := notifyEnv(t, map[string]string{
			"trackers":    `{"kinozal":{"mirrors":["kinozal.me","kinozal.cc"]}}`,
			"mirror_sync": `{"fails":{"kinozal":{"kinozal.cc":2}}}`,
		}, kinozalItem("kinozal.me", "kinozal.cc"), linksYAML("kinozal.tv", "kinozal.me"), "kinozal.tv", "kinozal.me")
		e.deps.ConfirmOn = false
		runOnce(context.Background(), e.deps)
		checkList(t, "retired", e.store.list(t, "kinozal", "retired"), "kinozal.cc")
		check(t, sent)
	})
	t.Run("confirm mode new unconfirmed mirror", func(t *testing.T) {
		e, sent := notifyEnv(t, map[string]string{"trackers": `{"kinozal":{"mirrors":["kinozal.me"]}}`}, kinozalItem("kinozal.me"),
			linksYAML("kinozal.tv", "kinozal.me", "kinozal.guru"), "kinozal.tv", "kinozal.me", "kinozal.guru")
		runOnce(context.Background(), e.deps)
		checkList(t, "unconfirmed", e.store.list(t, "kinozal", "unconfirmed"), "kinozal.guru")
		check(t, sent, "kinozal: новое зеркало kinozal.guru ждет подтверждения")
	})
	t.Run("mirrors off main suggestion is stored but silent", func(t *testing.T) {
		item := kinozalItem("kinozal.me")
		item.ActiveDomain = "kinozal.tv"
		e, sent := notifyEnv(t, map[string]string{"trackers": `{"kinozal":{"mirrors":["kinozal.me"],"active_domain":"kinozal.tv"}}`}, item,
			linksYAML("kinozal.tv", "kinozal.me"), "kinozal.me")
		e.deps.Check = func(_ context.Context, host string, _ int) (bool, string) {
			if settings.MirrorHostKey(host) == "kinozal.tv" {
				return false, "kinozal.me"
			}
			return true, ""
		}
		runOnce(context.Background(), e.deps)
		if got := len(e.store.doc(t).Suggestions["kinozal"]); got == 0 {
			t.Fatalf("stored suggestions = 0, want the main kept for when mirrors are on")
		}
		check(t, sent)
	})
	t.Run("mirrors off new mirror is stored but silent", func(t *testing.T) {
		item := kinozalItem("kinozal.me")
		item.ActiveDomain = "kinozal.tv"
		e, sent := notifyEnv(t, map[string]string{"trackers": `{"kinozal":{"mirrors":["kinozal.me"],"active_domain":"kinozal.tv"}}`}, item,
			linksYAML("kinozal.tv", "kinozal.me", "kinozal.guru"), "kinozal.tv", "kinozal.me", "kinozal.guru")
		runOnce(context.Background(), e.deps)
		checkList(t, "unconfirmed", e.store.list(t, "kinozal", "unconfirmed"), "kinozal.guru")
		check(t, sent)
	})
	t.Run("mirrors off remove suggestion is stored but silent", func(t *testing.T) {
		item := kinozalItem("kinozal.me", "kinozal.cc")
		item.ActiveDomain = "kinozal.tv"
		e, sent := notifyEnv(t, map[string]string{
			"trackers":    `{"kinozal":{"mirrors":["kinozal.me","kinozal.cc"],"active_domain":"kinozal.tv"}}`,
			"mirror_sync": `{"fails":{"kinozal":{"kinozal.cc":2}}}`,
		}, item, linksYAML("kinozal.tv", "kinozal.me"), "kinozal.tv", "kinozal.me")
		runOnce(context.Background(), e.deps)
		if got := len(e.store.doc(t).Suggestions["kinozal"]); got == 0 {
			t.Fatalf("stored suggestions = 0, want the remove kept for when mirrors are on")
		}
		check(t, sent)
	})
	t.Run("confirm mode remove suggestion once", func(t *testing.T) {
		e, sent := notifyEnv(t, map[string]string{
			"trackers":    `{"kinozal":{"mirrors":["kinozal.me","kinozal.cc"]}}`,
			"mirror_sync": `{"fails":{"kinozal":{"kinozal.cc":2}}}`,
		}, kinozalItem("kinozal.me", "kinozal.cc"), linksYAML("kinozal.tv", "kinozal.me"), "kinozal.tv", "kinozal.me")
		runOnce(context.Background(), e.deps)
		check(t, sent, "kinozal: убрать kinozal.cc (не отвечает)")
		runOnce(context.Background(), e.deps)
		check(t, sent, "kinozal: убрать kinozal.cc (не отвечает)")
	})
}

func TestRunOnceIfDue(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
		due  bool
	}{
		{"never ran", "", true},
		{"last run 25h ago", `{"last_run":"2026-10-22T11:00:00Z"}`, true},
		{"last run 1h ago", `{"last_run":"2026-10-23T11:00:00Z"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := map[string]string{"trackers": `{"kinozal":{"mirrors":["kinozal.me"]}}`}
			if tc.doc != "" {
				docs["mirror_sync"] = tc.doc
			}
			e := newEnv(t, docs, kinozalItem("kinozal.me"),
				linksYAML("kinozal.tv", "kinozal.me", "kinozal.guru"), "kinozal.tv", "kinozal.me", "kinozal.guru")
			e.idle = []bool{false}

			if got := RunOnceIfDue(context.Background(), e.deps); got != tc.due {
				t.Fatalf("ran = %v, want %v", got, tc.due)
			}
			if tc.due {
				checkList(t, "mirrors", e.store.list(t, "kinozal", "mirrors"), "kinozal.me", "kinozal.guru")
				if doc := e.store.doc(t); !doc.LastRun.Equal(e.now) {
					t.Errorf("last_run = %v, want %v", doc.LastRun, e.now)
				}
			} else if n := e.store.saveCount(); n != 0 {
				t.Errorf("saves = %d, want 0", n)
			}
			// приложение после этого вызова все равно перезапускается, ждать простоя и звать Reload нельзя
			if e.reloads != 0 || e.idleCalls != 0 {
				t.Errorf("reloads = %d, idle calls = %d, want 0", e.reloads, e.idleCalls)
			}
		})
	}
}

func TestRunOnceIfDueOutageKeepsDue(t *testing.T) {
	e := newEnv(t, map[string]string{
		"trackers":    `{"kinozal":{"mirrors":["kinozal.me"]}}`,
		"mirror_sync": `{"last_run":"2026-10-22T11:00:00Z","fails":{"kinozal":{"kinozal.me":1}}}`,
	}, kinozalItem("kinozal.me"), linksYAML("kinozal.tv", "kinozal.me", "kinozal.guru"))
	for i := range 2 {
		if !RunOnceIfDue(context.Background(), e.deps) {
			t.Fatalf("call %d: not run, want run", i+1)
		}
		doc := e.store.doc(t)
		if !doc.LastRun.Equal(time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)) {
			t.Errorf("call %d: last_run = %v, want unchanged", i+1, doc.LastRun)
		}
		if doc.Fails["kinozal"]["kinozal.me"] != 1 {
			t.Errorf("call %d: fails = %v, want unchanged", i+1, doc.Fails)
		}
	}
	// обычный прогон при сбое сети last_run пишет
	runOnce(context.Background(), e.deps)
	if doc := e.store.doc(t); !doc.LastRun.Equal(e.now) {
		t.Errorf("Run path last_run = %v, want %v", doc.LastRun, e.now)
	}
}

func TestRunOnceSourceFallback(t *testing.T) {
	e := newEnv(t, map[string]string{
		"trackers":       `{"kinozal":{"mirrors":["kinozal.me"]}}`,
		"trackers_added": `["kinozal","rutor"]`,
	}, kinozalItem("kinozal.me"), "", "kinozal.tv", "kinozal.me", "kinozal.guru")
	rutor := kinozalItem()
	e.deps.Items = config.Tracker{"kinozal": e.deps.Items["kinozal"], "rutor": &rutor}
	bad := statusStub(t, http.StatusInternalServerError)
	good := newHostStub(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + jackettRoot + kinozalPath, "/" + jackettRoot + "Definitions/rutor.yml":
			_, _ = w.Write([]byte(linksYAML("kinozal.tv", "kinozal.me", "kinozal.guru")))
		default:
			http.NotFound(w, r)
		}
	})
	e.deps.HTTP, e.deps.Sources = good.srv.Client(), tmpls(bad, good)
	before := logCount("WARN", "mirror sync: jackett unavailable")

	runOnce(context.Background(), e.deps)

	if got := logCount("WARN", "mirror sync: jackett unavailable") - before; got != 0 {
		t.Errorf("jackett warnings = %d, want 0", got)
	}
	doc := e.store.doc(t)
	for _, p := range []string{kinozalPath, "Definitions/rutor.yml"} {
		if len(doc.Sources[p].Links) != 3 {
			t.Errorf("source %s not cached: %+v", p, doc.Sources[p])
		}
	}
	if h := hitsOf(bad, good); !slices.Equal(h, []int32{1, 2}) {
		t.Errorf("hits bad/good = %v, want [1 2]", h)
	}
}
