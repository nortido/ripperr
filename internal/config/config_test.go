package config

import (
	"context"
	"testing"

	"github.com/sethvargo/go-envconfig"
)

func TestTrackerEnvOverridesDefaults(t *testing.T) {
	cfg := DefaultApp()
	err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target: &cfg,
		Lookuper: envconfig.MapLookuper(map[string]string{
			"TRACKER_KINOZAL_DOMAIN": "kinozal.guru",
			"TRACKER_RUTOR_ENABLED":  "false",
			"TRACKER_NNM_HOUR_LIMIT": "7",
		}),
	})
	if err != nil {
		t.Fatalf("ProcessWith: %v", err)
	}

	if cfg.Tracker.Kinozal.Domain != "kinozal.guru" {
		t.Errorf("Kinozal.Domain = %q, want env override kinozal.guru", cfg.Tracker.Kinozal.Domain)
	}
	if cfg.Tracker.Kinozal.LoginURL != "https://kinozal.tv/takelogin.php" {
		t.Errorf("Kinozal.LoginURL = %q, want untouched default", cfg.Tracker.Kinozal.LoginURL)
	}
	if cfg.Tracker.Rutor.Enabled != false {
		t.Errorf("Rutor.Enabled = %v, want false from env", cfg.Tracker.Rutor.Enabled)
	}
	if cfg.Tracker.NNM.HourLimit != 7 {
		t.Errorf("NNM.HourLimit = %d, want 7 from env", cfg.Tracker.NNM.HourLimit)
	}
	if cfg.Tracker.Rutracker.Domain != "rutracker.org" {
		t.Errorf("Rutracker.Domain = %q, want default rutracker.org", cfg.Tracker.Rutracker.Domain)
	}

	if it := (&cfg.Tracker).ByKey("teamhd"); it == nil || it.Name != "teamhd" {
		t.Errorf("ByKey(teamhd) = %+v, want Name teamhd", it)
	}
	if it := (&cfg.Tracker).ByKey("nope"); it != nil {
		t.Errorf("ByKey(nope) = %+v, want nil", it)
	}
}
