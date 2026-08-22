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

func TestRunnerValidateMagnetTimeoutBounds(t *testing.T) {
	base := DefaultApp().Runner
	// 0 - это "не задано" (пустое присваивание переменной окружения даёт
	// found=true, val=""), а не мусор: runner сам клампит его до минуты
	for _, v := range []int64{0, 5, 60, 600} {
		r := base
		r.MagnetTimeout = v
		if err := r.Validate(); err != nil {
			t.Errorf("MagnetTimeout=%d: unexpected error: %v", v, err)
		}
	}
	for _, v := range []int64{1, 4, 601} {
		r := base
		r.MagnetTimeout = v
		if err := r.Validate(); err == nil {
			t.Errorf("MagnetTimeout=%d: expected error", v)
		}
	}
}

func TestBlackholeDirPath(t *testing.T) {
	def := App{ConfigDir: "/config"}
	if got := def.BlackholeDirPath(); got != "/config/blackhole" {
		t.Errorf("default BlackholeDirPath = %q, want /config/blackhole", got)
	}

	override := App{ConfigDir: "/config", BlackholeDir: "/mnt/blackhole"}
	if got := override.BlackholeDirPath(); got != "/mnt/blackhole" {
		t.Errorf("overridden BlackholeDirPath = %q, want /mnt/blackhole", got)
	}
}
