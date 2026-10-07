package registry_test

import (
	"context"
	"slices"
	"testing"

	"github.com/sethvargo/go-envconfig"

	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
	_ "ripperr/internal/tracker/registry/all"
)

func TestBuiltinTrackersRegistered(t *testing.T) {
	want := []string{
		"rutracker", "kinozal", "rutor", "teamhd", "nnm",
		"toloka", "tapochek", "riperam", "booktracker", "anidub", "animelayer", "selezen", "rustorka", "aniliberty", "newstudio",
	}
	got := registry.Keys()
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("keys = %v, want %v (order is by id and is what the UI shows)", got, want)
		}
	}
}

func TestNewTrackersDisabledByDefault(t *testing.T) {
	tr := registry.ConfigDefaults()
	for _, k := range []string{"toloka", "tapochek", "riperam", "booktracker", "anidub", "animelayer", "selezen", "rustorka", "aniliberty", "newstudio"} {
		it := tr.ByKey(k)
		if it == nil {
			t.Errorf("%s has no defaults", k)
			continue
		}
		if it.Enabled {
			t.Errorf("%s is enabled by default", k)
		}
	}
}

func TestDefaultDomainsDoNotOverlap(t *testing.T) {
	defs := registry.All()
	for _, a := range defs {
		it := a.Defaults
		tr := a.New(trackerModel.Tracker{
			Name: it.Name, Domain: it.Domain, Mirrors: it.Mirrors, Publicity: it.Publicity,
			LoginURL: it.LoginURL, DownloadURL: it.DownloadURL,
		})
		for _, b := range defs {
			if a.Key == b.Key {
				continue
			}
			if tr.Detect("https://" + b.Defaults.Domain + "/x") {
				t.Errorf("%s detects the default domain of %s (%s)", a.Key, b.Key, b.Defaults.Domain)
			}
		}
	}
}

func TestIDKeyRoundTrip(t *testing.T) {
	for _, d := range registry.All() {
		if registry.ID(d.Key) != d.ID {
			t.Errorf("ID(%q) = %d, want %d", d.Key, registry.ID(d.Key), d.ID)
		}
		if registry.Key(d.ID) != d.Key {
			t.Errorf("Key(%d) = %q, want %q", d.ID, registry.Key(d.ID), d.Key)
		}
		if d.New == nil {
			t.Errorf("%s has no constructor", d.Key)
		}
	}
	if registry.ID("nope") != 0 {
		t.Errorf("ID(nope) = %d, want 0", registry.ID("nope"))
	}
	if registry.Key(0) != "" {
		t.Errorf("Key(0) = %q, want empty", registry.Key(0))
	}
}

func TestConfigDefaultsCarryTrackerValues(t *testing.T) {
	tr := registry.ConfigDefaults()
	if len(tr) != len(registry.All()) {
		t.Fatalf("defaults = %d entries, want %d", len(tr), len(registry.All()))
	}
	if got := tr.ByKey("rutracker"); got == nil || got.Domain != "rutracker.org" || got.HourLimit != 20 {
		t.Fatalf("rutracker defaults = %+v", got)
	}
	if got := tr.ByKey("rutor"); got == nil || got.Publicity != "public" || got.ProxyType != 0 {
		t.Fatalf("rutor defaults = %+v", got)
	}
	// каждый вызов отдает свою копию, правка не должна протечь в следующий
	tr.ByKey("rutracker").Domain = "mutated"
	if registry.ConfigDefaults().ByKey("rutracker").Domain != "rutracker.org" {
		t.Fatal("ConfigDefaults returns shared state")
	}
}

func TestConfigDefaultsMirrors(t *testing.T) {
	tr := registry.ConfigDefaults()
	want := map[string][]string{
		"rutor":      {"rutor.info"},
		"kinozal":    {"kinozal.me", "kinozal.guru"},
		"rutracker":  {"rutracker.net"},
		"selezen":    {"www.selezen.club"},
		"aniliberty": {"www.aniliberty.top"},
	}
	for key, mirrors := range want {
		got := tr.ByKey(key)
		if got == nil || !slices.Equal(got.Mirrors, mirrors) {
			t.Errorf("%s mirrors = %+v, want %v", key, got, mirrors)
		}
	}
}

func TestEnvOverridesRegistryDefaults(t *testing.T) {
	tr := registry.ConfigDefaults()
	err := config.ApplyTrackerEnv(context.Background(), tr, envconfig.MapLookuper(map[string]string{
		"TRACKER_KINOZAL_DOMAIN": "kinozal.guru",
		"TRACKER_RUTOR_ENABLED":  "false",
		"TRACKER_NNM_HOUR_LIMIT": "7",
	}))
	if err != nil {
		t.Fatalf("ApplyTrackerEnv: %v", err)
	}
	if tr.ByKey("kinozal").Domain != "kinozal.guru" {
		t.Errorf("kinozal.Domain = %q", tr.ByKey("kinozal").Domain)
	}
	if tr.ByKey("kinozal").LoginURL != "https://kinozal.tv/takelogin.php" {
		t.Errorf("kinozal.LoginURL = %q, want untouched default", tr.ByKey("kinozal").LoginURL)
	}
	if tr.ByKey("rutor").Enabled {
		t.Error("rutor.Enabled = true, want false from env")
	}
	if tr.ByKey("nnm").HourLimit != 7 {
		t.Errorf("nnm.HourLimit = %d, want 7", tr.ByKey("nnm").HourLimit)
	}
	if tr.ByKey("rutracker").Domain != "rutracker.org" {
		t.Errorf("rutracker.Domain = %q, want default", tr.ByKey("rutracker").Domain)
	}
}

func TestRegisterRejectsDuplicateID(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want panic on duplicate id, got none")
		}
	}()
	registry.Register(registry.Def{
		ID:  registry.ID("rutor"),
		Key: "clone",
		New: func(tr trackerModel.Tracker) trackerModel.ITracker { return &tr },
	})
}

func TestRegisterRejectsDuplicateKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want panic on duplicate key, got none")
		}
	}()
	registry.Register(registry.Def{
		ID:  9999,
		Key: "rutor",
		New: func(tr trackerModel.Tracker) trackerModel.ITracker { return &tr },
	})
}

func TestRegisterRejectsIncompleteDef(t *testing.T) {
	cases := []registry.Def{
		{ID: 0, Key: "zero", New: func(tr trackerModel.Tracker) trackerModel.ITracker { return &tr }},
		{ID: 9001, Key: "", New: func(tr trackerModel.Tracker) trackerModel.ITracker { return &tr }},
		{ID: 9002, Key: "nonew"},
	}
	for _, d := range cases {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("want panic for %+v, got none", d)
				}
			}()
			registry.Register(d)
		}()
	}
}
