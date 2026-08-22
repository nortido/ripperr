package config

import (
	"context"
	"testing"

	"github.com/sethvargo/go-envconfig"
)

func TestApplyTrackerEnv(t *testing.T) {
	tr := Tracker{
		"kinozal": &TrackerItem{Name: "kinozal", Domain: "kinozal.tv", LoginURL: "https://kinozal.tv/takelogin.php"},
		"rutor":   &TrackerItem{Name: "rutor", Enabled: true},
		"nnm":     &TrackerItem{Name: "nnm", HourLimit: 25},
	}
	err := ApplyTrackerEnv(context.Background(), tr, envconfig.MapLookuper(map[string]string{
		"TRACKER_KINOZAL_DOMAIN": "kinozal.guru",
		"TRACKER_RUTOR_ENABLED":  "false",
		"TRACKER_NNM_HOUR_LIMIT": "7",
	}))
	if err != nil {
		t.Fatalf("ApplyTrackerEnv: %v", err)
	}

	if tr["kinozal"].Domain != "kinozal.guru" {
		t.Errorf("kinozal.Domain = %q, want env override kinozal.guru", tr["kinozal"].Domain)
	}
	if tr["kinozal"].LoginURL != "https://kinozal.tv/takelogin.php" {
		t.Errorf("kinozal.LoginURL = %q, want untouched default", tr["kinozal"].LoginURL)
	}
	if tr["rutor"].Enabled {
		t.Error("rutor.Enabled = true, want false from env")
	}
	if tr["nnm"].HourLimit != 7 {
		t.Errorf("nnm.HourLimit = %d, want 7 from env", tr["nnm"].HourLimit)
	}

	if it := tr.ByKey("nnm"); it == nil || it.Name != "nnm" {
		t.Errorf("ByKey(nnm) = %+v, want Name nnm", it)
	}
	if it := tr.ByKey("nope"); it != nil {
		t.Errorf("ByKey(nope) = %+v, want nil", it)
	}
}

// префикс собирается из ключа, поэтому чужие переменные не должны утекать в трекер
func TestApplyTrackerEnvIgnoresForeignPrefix(t *testing.T) {
	tr := Tracker{"rutor": &TrackerItem{Name: "rutor", Domain: "rutor.is"}}
	err := ApplyTrackerEnv(context.Background(), tr, envconfig.MapLookuper(map[string]string{
		"TRACKER_KINOZAL_DOMAIN": "kinozal.guru",
	}))
	if err != nil {
		t.Fatalf("ApplyTrackerEnv: %v", err)
	}
	if tr["rutor"].Domain != "rutor.is" {
		t.Errorf("rutor.Domain = %q, want rutor.is", tr["rutor"].Domain)
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

// App.Tracker это map без тега env: envconfig.Process обязан пройти мимо него,
// не уронив разбор и не обнулив уже собранный набор
func TestProcessLeavesTrackerMapAlone(t *testing.T) {
	cfg := DefaultApp()
	cfg.Tracker = Tracker{"rutor": &TrackerItem{Name: "rutor", Domain: "rutor.is"}}
	err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.MapLookuper(map[string]string{"LOG_LEVEL": "debug"}),
	})
	if err != nil {
		t.Fatalf("ProcessWith: %v", err)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("Log.Level = %q, want debug", cfg.Log.Level)
	}
	if len(cfg.Tracker) != 1 || cfg.Tracker["rutor"].Domain != "rutor.is" {
		t.Fatalf("Tracker = %+v, want untouched", cfg.Tracker)
	}
}
