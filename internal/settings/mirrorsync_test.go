package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ripperr/internal/config"
)

func TestMirrorHostKey(t *testing.T) {
	cases := map[string]string{
		" WWW.Kinozal.TV ":  "kinozal.tv",
		"www.www.a.example": "www.a.example",
		"a.example":         "a.example",
		"wwwa.example":      "wwwa.example",
		"":                  "",
	}
	for in, want := range cases {
		if got := MirrorHostKey(in); got != want {
			t.Errorf("MirrorHostKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidMirrorHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"kinozal.tv", true},
		{"use.selezen.club", true},
		{"Rutor.IS", true},
		{"xn--e1afmkfd.xn--p1ai", true},
		{"evil.example:8080", false},
		{"127.0.0.1", false},
		{"[::1]:81", false},
		{"a.example.", false},
		{":80", false},
		{"пример.рф", false},
		{"kinozal.tv:443", false},
		{"a..b.example", false},
		{"localhost", false},
		{".a.example", false},
		{"", false},
		{"a.example/path", false},
	}
	for _, tc := range cases {
		if got := ValidMirrorHost(tc.host); got != tc.want {
			t.Errorf("ValidMirrorHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

func TestMirrorSyncPending(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	recent := MirrorAuto{Kind: MirrorAutoAdded, Host: "x.kinozal.tv", At: now.Add(-29 * 24 * time.Hour)}
	old := MirrorAuto{Kind: MirrorAutoRemoved, Host: "y.kinozal.tv", Reason: MirrorReasonDead, At: now.Add(-31 * 24 * time.Hour)}
	mainSugg := MirrorSuggestion{Kind: MirrorKindMain, Host: "kinozal.me", From: "kinozal.tv", Reason: MirrorReasonRedirect}
	removeSugg := MirrorSuggestion{Kind: MirrorKindRemove, Host: "dead.kinozal.org", Reason: MirrorReasonDead}
	doc := MirrorSync{
		Suggestions: map[string][]MirrorSuggestion{
			"kinozal": {
				mainSugg,
				{Kind: MirrorKindMain, Host: "kinozal.guru", From: "old.example", Reason: MirrorReasonJackett},
				{Kind: MirrorKindMain, Host: "www.kinozal.tv", From: "kinozal.tv", Reason: MirrorReasonRedirect},
				removeSugg,
				{Kind: MirrorKindRemove, Host: "kinozal.tv", Reason: MirrorReasonDead},
				{Kind: MirrorKindRemove, Host: "notamirror.example", Reason: MirrorReasonJackett},
			},
			"rutor": {{Kind: MirrorKindMain, Host: "rutor.info", From: "WWW.rutor.is", Reason: MirrorReasonDead}},
		},
		Auto:   map[string][]MirrorAuto{"kinozal": {old, recent}},
		Paused: map[string]MirrorPause{"kinozal": {Host: "new.kinozal.xyz", Since: now}, "rutor": {Host: "rutor.example", Since: now}},
	}
	kinozal := config.TrackerItem{
		Domain:      "kinozal.tv",
		Mirrors:     []string{"kinozal.me", "new.kinozal.xyz", "dead.kinozal.org"},
		Unconfirmed: []string{"gone.kinozal.net", "New.kinozal.xyz"},
	}
	rutor := config.TrackerItem{Domain: "rutor.is", Mirrors: []string{"rutor.info", "rutor.example"}}
	cases := []struct {
		name      string
		key       string
		item      config.TrackerItem
		confirmOn bool
		want      MirrorPending
	}{
		{"confirm on", "kinozal", kinozal, true, MirrorPending{
			Suggestions: []MirrorSuggestion{{Kind: MirrorKindConfirm, Host: "New.kinozal.xyz"}, mainSugg, removeSugg},
			Paused:      "new.kinozal.xyz",
			Auto:        []MirrorAuto{recent},
		}},
		{"confirm off", "kinozal", kinozal, false, MirrorPending{
			Suggestions: []MirrorSuggestion{mainSugg},
			Auto:        []MirrorAuto{recent},
		}},
		{"paused host confirmed", "rutor", rutor, true, MirrorPending{
			Suggestions: []MirrorSuggestion{doc.Suggestions["rutor"][0]},
		}},
		{"unknown key", "nnm", config.TrackerItem{Domain: "nnmclub.to"}, true, MirrorPending{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := doc.Pending(tc.key, tc.item, tc.confirmOn); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Pending =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

func TestMirrorSyncDismiss(t *testing.T) {
	ctx := context.Background()
	db := &fakeSettingsDB{}
	item := config.TrackerItem{Domain: "kinozal.tv", Mirrors: []string{"dead.kinozal.org"}}
	seed := func(d *MirrorSync) {
		d.Suggestions = map[string][]MirrorSuggestion{"kinozal": {
			{Kind: MirrorKindMain, Host: "kinozal.me", From: "kinozal.tv", Reason: MirrorReasonRedirect},
			{Kind: MirrorKindRemove, Host: "dead.kinozal.org", Reason: MirrorReasonDead},
		}}
	}
	if err := UpdateMirrorSync(ctx, db, seed); err != nil {
		t.Fatal(err)
	}
	var found bool
	if err := UpdateMirrorSync(ctx, db, func(d *MirrorSync) { found = d.Dismiss("kinozal", MirrorKindRemove, "WWW.Dead.kinozal.org ") }); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("Dismiss of a stored remove suggestion = false")
	}
	d, err := GetMirrorSync(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.Dismissed, []string{"kinozal|remove|dead.kinozal.org"}) {
		t.Errorf("dismissed = %v", d.Dismissed)
	}
	if got := d.Pending("kinozal", item, true).Suggestions; len(got) != 1 || got[0].Kind != MirrorKindMain {
		t.Errorf("pending after dismiss = %+v", got)
	}
	saves := db.saves
	for _, c := range [][2]string{{MirrorKindRemove, "dead.kinozal.org"}, {MirrorKindConfirm, "kinozal.me"}, {MirrorKindMain, "other.example"}, {"bogus", "kinozal.me"}} {
		if err := UpdateMirrorSync(ctx, db, func(d *MirrorSync) { found = d.Dismiss("kinozal", c[0], c[1]) }); err != nil {
			t.Fatal(err)
		}
		if found {
			t.Errorf("Dismiss(%s, %s) = true, want false", c[0], c[1])
		}
	}
	if db.saves != saves {
		t.Errorf("saves = %d after no-op dismisses, want %d", db.saves, saves)
	}
}

func TestSetSuggestionsSkipsDismissed(t *testing.T) {
	var d MirrorSync
	d.Dismissed = []string{"kinozal|remove|dead.kinozal.org"}
	mainSugg := MirrorSuggestion{Kind: MirrorKindMain, Host: "kinozal.me", From: "kinozal.tv", Reason: MirrorReasonRedirect}
	d.SetSuggestions("kinozal", []MirrorSuggestion{
		{Kind: MirrorKindRemove, Host: "www.dead.kinozal.org", Reason: MirrorReasonDead},
		mainSugg,
	})
	if got := d.Suggestions["kinozal"]; !reflect.DeepEqual(got, []MirrorSuggestion{mainSugg}) {
		t.Errorf("suggestions = %+v", got)
	}
	d.SetSuggestions("kinozal", []MirrorSuggestion{{Kind: MirrorKindRemove, Host: "dead.kinozal.org", Reason: MirrorReasonDead}})
	if _, ok := d.Suggestions["kinozal"]; ok {
		t.Errorf("key kept with only dismissed items: %+v", d.Suggestions)
	}
}

func TestMirrorSyncRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := &fakeSettingsDB{}
	if d, err := GetMirrorSync(ctx, db); err != nil || !reflect.DeepEqual(d, MirrorSync{}) {
		t.Fatalf("absent: %+v, %v", d, err)
	}
	db.data = map[string]json.RawMessage{"mirror_sync": json.RawMessage(`{"fails":[`)}
	if d, err := GetMirrorSync(ctx, db); err != nil || !reflect.DeepEqual(d, MirrorSync{}) {
		t.Fatalf("invalid: %+v, %v", d, err)
	}
	if !hasWarn("settings mirror_sync: invalid JSON") {
		t.Error("no WARN for invalid mirror_sync")
	}
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	want := MirrorSync{
		LastRun:     at,
		Sources:     map[string]MirrorSource{"Definitions/kinozal.yml": {Links: []string{"https://kinozal.tv/"}, Legacy: []string{"https://kinozal.me/"}, Fetched: at}},
		Fails:       map[string]map[string]int{"kinozal": {"kinozal.me": 2}},
		Suggestions: map[string][]MirrorSuggestion{"kinozal": {{Kind: MirrorKindRemove, Host: "kinozal.me", Reason: MirrorReasonJackett}}},
		Dismissed:   []string{"kinozal|main|kinozal.guru"},
		Auto:        map[string][]MirrorAuto{"kinozal": {{Kind: MirrorAutoAdded, Host: "kinozal.xyz", At: at}}},
		Paused:      map[string]MirrorPause{"kinozal": {Host: "kinozal.xyz", Since: at}},
		Pruned:      map[string][]MirrorPruned{"kinozal": {{Host: "kinozal.old", At: at}}},
	}
	if err := UpdateMirrorSync(ctx, db, func(d *MirrorSync) { *d = want }); err != nil {
		t.Fatal(err)
	}
	raw := string(db.data["mirror_sync"])
	for _, k := range []string{`"last_run":`, `"sources":{"Definitions/kinozal.yml":{"links":`, `"legacy":`, `"fetched":`, `"fails":{"kinozal":{"kinozal.me":2}}`, `"suggestions":{"kinozal":[{"kind":"remove","host":"kinozal.me","reason":"jackett"}]}`, `"dismissed":["kinozal|main|kinozal.guru"]`, `"auto":{"kinozal":[{"kind":"added","host":"kinozal.xyz","at":`, `"paused":{"kinozal":{"host":"kinozal.xyz","since":`, `"pruned":{"kinozal":[{"host":"kinozal.old","at":`} {
		if !strings.Contains(raw, k) {
			t.Errorf("stored doc lacks %s: %s", k, raw)
		}
	}
	got, err := GetMirrorSync(ctx, db)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip:\n%+v\n%v\nwant\n%+v", got, err, want)
	}
	saves := db.saves
	if err := UpdateMirrorSync(ctx, db, func(d *MirrorSync) { d.SetSuggestions("kinozal", d.Suggestions["kinozal"]) }); err != nil {
		t.Fatal(err)
	}
	if db.saves != saves {
		t.Errorf("unchanged update wrote: saves %d, want %d", db.saves, saves)
	}
}

type slowSettingsDB struct {
	mu   sync.Mutex
	data map[string]json.RawMessage
}

func (s *slowSettingsDB) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	s.mu.Lock()
	v, ok := s.data[key]
	s.mu.Unlock()
	time.Sleep(2 * time.Millisecond)
	return v, ok, nil
}

func (s *slowSettingsDB) SaveSetting(_ context.Context, key string, value json.RawMessage) error {
	time.Sleep(2 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = make(map[string]json.RawMessage)
	}
	s.data[key] = value
	return nil
}

func TestUpdateMirrorSyncSerializes(t *testing.T) {
	ctx := context.Background()
	db := &slowSettingsDB{}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			id := fmt.Sprintf("k|remove|h%d.example", i)
			if err := UpdateMirrorSync(ctx, db, func(d *MirrorSync) { d.Dismissed = append(d.Dismissed, id) }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	d, err := GetMirrorSync(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(d.Dismissed)
	if got := len(slices.Compact(d.Dismissed)); got != 20 {
		t.Errorf("ids = %d, want 20: %v", got, d.Dismissed)
	}
}

func TestMirrorSyncIsDismissed(t *testing.T) {
	d := MirrorSync{Dismissed: []string{"kinozal|remove|dead.kinozal.org"}}
	cases := []struct {
		key, kind, host string
		want            bool
	}{
		{"kinozal", MirrorKindRemove, "dead.kinozal.org", true},
		{"kinozal", MirrorKindRemove, " WWW.Dead.Kinozal.org ", true},
		{"kinozal", MirrorKindMain, "dead.kinozal.org", false},
		{"rutor", MirrorKindRemove, "dead.kinozal.org", false},
		{"kinozal", MirrorKindRemove, "other.kinozal.org", false},
	}
	for _, c := range cases {
		if got := d.IsDismissed(c.key, c.kind, c.host); got != c.want {
			t.Errorf("IsDismissed(%q, %q, %q) = %v, want %v", c.key, c.kind, c.host, got, c.want)
		}
	}
}
