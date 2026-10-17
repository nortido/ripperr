package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/helper/formcreds"
	"ripperr/internal/model/tracker"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker/registry"
)

type migrateDB struct {
	data       map[string]json.RawMessage
	saves      []string
	rows       map[int]bool
	trackerErr error
}

func (f *migrateDB) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	v, ok := f.data[key]
	return v, ok, nil
}

func (f *migrateDB) SaveSetting(_ context.Context, key string, value json.RawMessage) error {
	if f.data == nil {
		f.data = make(map[string]json.RawMessage)
	}
	f.data[key] = value
	f.saves = append(f.saves, key)
	return nil
}

func (f *migrateDB) GetTracker(_ context.Context, id int) (*tracker.Tracker, error) {
	if f.trackerErr != nil {
		return nil, f.trackerErr
	}
	return &tracker.Tracker{TypeID: id, Exists: f.rows[id]}, nil
}

func (f *migrateDB) savedCount(key string) int {
	n := 0
	for _, k := range f.saves {
		if k == key {
			n++
		}
	}
	return n
}

func defaultBase() config.App {
	return config.App{Tracker: registry.ConfigDefaults()}
}

func TestMigrateSettingsConvertsCredentials(t *testing.T) {
	const (
		rutrackerOld = "login_username=va+sya&login_password=SEC-p%40ss%26&login=%D0%B2%D1%85%D0%BE%D0%B4"
		overlay      = `{"rutracker":{"credentials":"` + rutrackerOld + `","cookies":"c=1","enabled":true},` +
			`"kinozal":{"credentials":"username={login}&password={password}&to=%2F","login":"k","password":"SEC-q"},` +
			`"nnm":{"credentials":"raw=1"},` +
	)
	db := &migrateDB{data: map[string]json.RawMessage{"trackers": json.RawMessage(overlay)}}
	logStart := len(logger.Snapshot(1000))

	if err := MigrateSettings(context.Background(), db, defaultBase()); err != nil {
		t.Fatal(err)
	}
	if n := db.savedCount("trackers"); n != 1 {
		t.Fatalf("trackers saved %d times, want 1", n)
	}

	var before, after map[string]json.RawMessage
	if err := json.Unmarshal([]byte(overlay), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(db.data["trackers"], &after); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"kinozal", "nnm"} {
		if string(after[key]) != string(before[key]) {
			t.Fatalf("%s entry changed: %s, was %s", key, after[key], before[key])
		}
	}

	var ru map[string]json.RawMessage
	if err := json.Unmarshal(after["rutracker"], &ru); err != nil {
		t.Fatal(err)
	}
	var creds, login, password string
	for field, dst := range map[string]*string{"credentials": &creds, "login": &login, "password": &password} {
		if err := json.Unmarshal(ru[field], dst); err != nil {
			t.Fatalf("rutracker %s: %v", field, err)
		}
	}
	if want := "login_username={login}&login_password={password}&login=%D0%B2%D1%85%D0%BE%D0%B4"; creds != want {
		t.Fatalf("credentials = %q, want %q", creds, want)
	}
	if login != "va sya" || password != "SEC-p@ss&" {
		t.Fatalf("login, password = %q, %q", login, password)
	}
	if got := formcreds.Expand(creds, login, password); got != rutrackerOld {
		t.Fatalf("expanded = %q, want %q", got, rutrackerOld)
	}
	if string(ru["cookies"]) != `"c=1"` || string(ru["enabled"]) != "true" {
		t.Fatalf("other fields changed: %s", after["rutracker"])
	}

	entries := logger.Snapshot(1000)[logStart:]
	var warned bool
	for _, e := range entries {
		if e.Level == "WARN" && strings.Contains(e.Message) {
			warned = true
		}
		for _, secret := range []string{"SEC-", "a@b", "va sya", "va+sya", "p@ss"} {
			if strings.Contains(e.Message, secret) {
				t.Fatalf("log %q carries %q", e.Message, secret)
			}
		}
	}
	if !warned {
	}

	if err := MigrateSettings(context.Background(), db, defaultBase()); err != nil {
		t.Fatal(err)
	}
	if n := db.savedCount("trackers"); n != 1 {
		t.Fatalf("second run saved trackers again, %d saves", n)
	}
}

func TestMigrateSettingsBootstrapsAddedList(t *testing.T) {
	ctx := context.Background()

	t.Run("superset", func(t *testing.T) {
		base := defaultBase()
		base.Tracker["toloka"].Credentials = "x=1"
		db := &migrateDB{
			data: map[string]json.RawMessage{"trackers": json.RawMessage(`{"kinozal":{"enabled":false}}`)},
			rows: map[int]bool{1: true, 3: true},
		}
		if err := MigrateSettings(ctx, db, base); err != nil {
			t.Fatal(err)
		}
		var got []string
		if err := json.Unmarshal(db.data["trackers_added"], &got); err != nil {
			t.Fatal(err)
		}
		if want := []string{"rutracker", "kinozal", "rutor", "toloka"}; !slices.Equal(got, want) {
			t.Fatalf("added = %v, want %v", got, want)
		}
		if n := db.savedCount("trackers"); n != 0 {
			t.Fatalf("trackers saved %d times", n)
		}
	})

	t.Run("fresh install", func(t *testing.T) {
		db := &migrateDB{}
		if err := MigrateSettings(ctx, db, defaultBase()); err != nil {
			t.Fatal(err)
		}
		if got := string(db.data["trackers_added"]); got != "[]" {
			t.Fatalf("added = %s, want []", got)
		}
	})

	t.Run("present document", func(t *testing.T) {
		db := &migrateDB{
			data: map[string]json.RawMessage{
				"trackers":       json.RawMessage(`{"kinozal":{"enabled":true}}`),
			},
			rows: map[int]bool{1: true},
		}
		if err := MigrateSettings(ctx, db, defaultBase()); err != nil {
			t.Fatal(err)
		}
		if len(db.saves) != 0 {
			t.Fatalf("saved %v", db.saves)
		}
	})

	t.Run("tracker row error", func(t *testing.T) {
		db := &migrateDB{trackerErr: errors.New("db down")}
		if err := MigrateSettings(ctx, db, defaultBase()); err == nil {
			t.Fatal("no error")
		}
		if n := db.savedCount("trackers_added"); n != 0 {
			t.Fatal("trackers_added written")
		}
	})

	t.Run("invalid overlay", func(t *testing.T) {
		db := &migrateDB{
			data: map[string]json.RawMessage{"trackers": json.RawMessage(`{"rutracker":`)},
			rows: map[int]bool{1: true},
		}
		if err := MigrateSettings(ctx, db, defaultBase()); err == nil {
			t.Fatal("no error")
		}
		if len(db.saves) != 0 {
			t.Fatalf("saved %v", db.saves)
		}
	})
}

func TestMigrateSettingsKeepsNonUTF8Credentials(t *testing.T) {
	const overlay = `{"rutracker":{"credentials":"login_username=%C2%E0%F1%FF&login_password=SEC-w&login=%D0%B2%D1%85%D0%BE%D0%B4"}}`
	db := &migrateDB{data: map[string]json.RawMessage{"trackers": json.RawMessage(overlay)}}
	logStart := len(logger.Snapshot(1000))

	if err := MigrateSettings(context.Background(), db, defaultBase()); err != nil {
		t.Fatal(err)
	}
	if n := db.savedCount("trackers"); n != 0 || string(db.data["trackers"]) != overlay {
		t.Fatalf("trackers saved %d times: %s", n, db.data["trackers"])
	}
	var warned bool
	for _, e := range logger.Snapshot(1000)[logStart:] {
		if e.Level == "WARN" && strings.Contains(e.Message, "rutracker") {
			warned = true
		}
		if strings.Contains(e.Message, "SEC-") || strings.Contains(e.Message, "%C2") {
			t.Fatalf("log %q carries a value", e.Message)
		}
	}
	if !warned {
		t.Fatal("no WARN for rutracker")
	}
}
