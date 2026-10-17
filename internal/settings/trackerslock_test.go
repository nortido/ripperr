package settings

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"ripperr/internal/config"
)

func trackersDB(trackers string, added ...string) *fakeSettingsDB {
	db := &fakeSettingsDB{data: map[string]json.RawMessage{"trackers": json.RawMessage(trackers)}}
	if added != nil {
		b, _ := json.Marshal(added)
		db.data["trackers_added"] = b
	}
	return db
}

func storedEntries(t *testing.T, db *fakeSettingsDB) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(db.data["trackers"], &m); err != nil {
		t.Fatalf("trackers doc: %v", err)
	}
	return m
}

func storedEntry(t *testing.T, db *fakeSettingsDB, key string) map[string]json.RawMessage {
	t.Helper()
	var e map[string]json.RawMessage
	if err := json.Unmarshal(storedEntries(t, db)[key], &e); err != nil {
		t.Fatalf("entry %s: %v", key, err)
	}
	return e
}

func rawList(t *testing.T, e map[string]json.RawMessage, field string) []string {
	t.Helper()
	raw, ok := e[field]
	if !ok {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s: %v", field, err)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func wantList(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) || (got == nil) != (want == nil) {
		t.Errorf("%s = %#v, want %#v", name, got, want)
	}
}

var rutorBase = config.TrackerItem{Name: "rutor", Domain: "rutor.is", Mirrors: []string{"rutor.info"}, Credentials: "base:secret", HourLimit: 20}

func TestMaintainAppendsAtEnd(t *testing.T) {
	ctx := context.Background()
	db := trackersDB(`{"rutor":{"domain":"c.example","mirrors":["a.example"],"retired":["r.example"]}}`, "rutor")
	before := storedEntry(t, db, "rutor")
	added, retired, pruned, err := MaintainTrackerMirrors(ctx, db, "rutor", rutorBase,
		[]string{"b.example", "A.example", "www.c.example", "r.example", "evil.example:8080"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantList(t, "added", added, []string{"b.example"})
	wantList(t, "retired", retired, nil)
	wantList(t, "pruned", pruned, nil)
	e := storedEntry(t, db, "rutor")
	wantList(t, "mirrors", rawList(t, e, "mirrors"), []string{"a.example", "b.example"})
	wantList(t, "unconfirmed", rawList(t, e, "unconfirmed"), []string{"b.example"})
	for _, k := range []string{"domain", "retired"} {
		if string(e[k]) != string(before[k]) {
			t.Errorf("%s = %s, want %s", k, e[k], before[k])
		}
	}
	if _, ok := e["active_domain"]; ok {
		t.Error("active_domain written without a change")
	}
	saves := db.saves
	added, _, _, err = MaintainTrackerMirrors(ctx, db, "rutor", rutorBase, []string{"b.example", "a.example"}, nil, nil)
	if err != nil || added != nil {
		t.Fatalf("second run: added %v, err %v", added, err)
	}
	if db.saves != saves {
		t.Errorf("second run wrote: saves %d, want %d", db.saves, saves)
	}
}

func TestMaintainOverlayWithoutMirrorsUsesBase(t *testing.T) {
	db := trackersDB(`{"rutor":{"hour_limit":3}}`, "rutor")
	base := rutorBase
	base.Mirrors = []string{"d1.example", "d2.example"}
	added, _, _, err := MaintainTrackerMirrors(context.Background(), db, "rutor", base, []string{"b.example", "d2.example"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantList(t, "added", added, []string{"b.example"})
	e := storedEntry(t, db, "rutor")
	wantList(t, "mirrors", rawList(t, e, "mirrors"), []string{"d1.example", "d2.example", "b.example"})
	wantList(t, "unconfirmed", rawList(t, e, "unconfirmed"), []string{"b.example"})
	if got := slices.Sorted(maps.Keys(e)); !slices.Equal(got, []string{"hour_limit", "mirrors", "unconfirmed"}) {
		t.Errorf("entry keys = %v (base values must not leak into the doc)", got)
	}
}

func TestMaintainKeepsOtherFieldsByteIdentical(t *testing.T) {
	ctx := context.Background()
	doc := `{"kinozal":{"domain":"kinozal.tv","password":"k&z<q>","cookies":"a=1"},` +
		`"rutor":{"credentials":"u:p&<x>","login":"user","password":"p&ss\"q","cookies":"sid=1; a=<b>","hour_limit":7,"mirrors":["a.example"],"x_future":{"a":[1,2]}}}`
	db := trackersDB(doc)
	before := storedEntry(t, db, "rutor")
	kinozal := string(storedEntries(t, db)["kinozal"])
	if _, _, _, err := MaintainTrackerMirrors(ctx, db, "rutor", rutorBase, []string{"b.example"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := MaintainTrackerMirrors(ctx, db, "rutor", rutorBase, nil, []string{"a.example"}, nil); err != nil {
		t.Fatal(err)
	}
	e := storedEntry(t, db, "rutor")
	for _, k := range []string{"credentials", "login", "password", "cookies", "hour_limit", "x_future"} {
		if string(e[k]) != string(before[k]) {
			t.Errorf("%s changed", k)
		}
	}
	wantList(t, "mirrors", rawList(t, e, "mirrors"), []string{"b.example"})
	wantList(t, "retired", rawList(t, e, "retired"), []string{"a.example"})
	if got := string(storedEntries(t, db)["kinozal"]); got != kinozal {
		t.Error("untouched entry kinozal changed")
	}
}

func TestMaintainRetire(t *testing.T) {
	db := trackersDB(`{"rutor":{"domain":"rutor.is","active_domain":"b.example","mirrors":["a.example","b.example"],"unconfirmed":["b.example"],"retired":["old.example"]}}`, "rutor")
	_, retired, _, err := MaintainTrackerMirrors(context.Background(), db, "rutor", rutorBase, nil,
		[]string{"B.example", "rutor.is", "www.rutor.is", "missing.example"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantList(t, "retired result", retired, []string{"b.example"})
	e := storedEntry(t, db, "rutor")
	wantList(t, "mirrors", rawList(t, e, "mirrors"), []string{"a.example"})
	wantList(t, "unconfirmed", rawList(t, e, "unconfirmed"), []string{})
	wantList(t, "retired", rawList(t, e, "retired"), []string{"old.example", "b.example"})
	if got := string(e["active_domain"]); got != `""` {
		t.Errorf("active_domain = %s, want empty", got)
	}
	if got := string(e["domain"]); got != `"rutor.is"` {
		t.Errorf("domain = %s", got)
	}
}

func TestMaintainPrune(t *testing.T) {
	db := trackersDB(`{"rutor":{"credentials":"c&d","mirrors":["back.example"],"retired":["old.example","keep.example","back.example"],"hour_limit":4}}`, "rutor")
	before := storedEntry(t, db, "rutor")
	_, _, pruned, err := MaintainTrackerMirrors(context.Background(), db, "rutor", rutorBase, nil, nil,
		[]string{"old.example", "absent.example", "back.example"})
	if err != nil {
		t.Fatal(err)
	}
	wantList(t, "pruned", pruned, []string{"old.example"})
	e := storedEntry(t, db, "rutor")
	wantList(t, "retired", rawList(t, e, "retired"), []string{"keep.example", "back.example"})
	if got, want := slices.Sorted(maps.Keys(e)), slices.Sorted(maps.Keys(before)); !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	for k, v := range before {
		if k != "retired" && string(e[k]) != string(v) {
			t.Errorf("%s = %s, want %s", k, e[k], v)
		}
	}
}

func TestMaintainSkipsNotAdded(t *testing.T) {
	ctx := context.Background()
	db := trackersDB(`{"rutor":{"mirrors":["a.example"]},"kinozal":{"mirrors":[]}}`, "kinozal")
	added, retired, pruned, err := MaintainTrackerMirrors(ctx, db, "rutor", rutorBase, []string{"b.example"}, []string{"a.example"}, nil)
	if err != nil || added != nil || retired != nil || pruned != nil || db.saves != 0 {
		t.Fatalf("not added: %v %v %v %v saves %d", added, retired, pruned, err, db.saves)
	}
	kinozal := config.TrackerItem{Domain: "kinozal.tv"}
	if added, _, _, err = MaintainTrackerMirrors(ctx, db, "kinozal", kinozal, []string{"kinozal.me"}, nil, nil); err != nil || len(added) != 1 {
		t.Fatalf("added tracker: %v %v", added, err)
	}
	db = trackersDB(`{}`)
	if added, _, _, err = MaintainTrackerMirrors(ctx, db, "kinozal", kinozal, []string{"kinozal.me"}, nil, nil); err != nil || len(added) != 1 {
		t.Fatalf("no trackers_added doc: %v %v", added, err)
	}
}

func TestMaintainWaitsForConcurrentPut(t *testing.T) {
	ctx := context.Background()
	db := trackersDB(`{"rutor":{"mirrors":["a.example"],"hour_limit":2}}`, "rutor")
	locked := make(chan struct{})
	started := make(chan struct{})
	putDone := make(chan error, 1)
	var merged atomic.Bool
	go func() {
		unlock := LockTrackers()
		close(locked)
		<-started
		time.Sleep(50 * time.Millisecond)
		err := MergeTrackerKey(ctx, db, "rutor", json.RawMessage(`{"mirrors":["z.example"],"hour_limit":5}`))
		merged.Store(true)
		unlock()
		putDone <- err
	}()
	<-locked
	close(started)
	unlock := LockTrackers()
	sawPut := merged.Load()
	_, _, _, err := MaintainTrackerMirrors(ctx, db, "rutor", rutorBase, []string{"b.example"}, nil, nil)
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = <-putDone; err != nil {
		t.Fatal(err)
	}
	if !sawPut {
		t.Error("LockTrackers returned while the PUT held it")
	}
	e := storedEntry(t, db, "rutor")
	wantList(t, "mirrors", rawList(t, e, "mirrors"), []string{"z.example", "b.example"})
	if got := string(e["hour_limit"]); got != "5" {
		t.Errorf("hour_limit = %s, want 5", got)
	}
}

func TestConfirmTrackerMirror(t *testing.T) {
	ctx := context.Background()
	db := trackersDB(`{"rutor":{"password":"x&y","mirrors":["a.example","b.example"],"unconfirmed":["a.example","b.example"]}}`, "rutor")
	before := storedEntry(t, db, "rutor")
	ok, err := ConfirmTrackerMirror(ctx, db, "rutor", " A.example")
	if err != nil || !ok {
		t.Fatalf("confirm: %v %v", ok, err)
	}
	e := storedEntry(t, db, "rutor")
	wantList(t, "unconfirmed", rawList(t, e, "unconfirmed"), []string{"b.example"})
	for _, k := range []string{"password", "mirrors"} {
		if string(e[k]) != string(before[k]) {
			t.Errorf("%s changed", k)
		}
	}
	saves := db.saves
	for _, c := range [][2]string{{"rutor", "a.example"}, {"rutor", "c.example"}, {"kinozal", "a.example"}} {
		if ok, err = ConfirmTrackerMirror(ctx, db, c[0], c[1]); err != nil || ok {
			t.Errorf("confirm %s %s: %v %v, want false", c[0], c[1], ok, err)
		}
	}
	if db.saves != saves {
		t.Errorf("absent confirms wrote: saves %d, want %d", db.saves, saves)
	}
}
