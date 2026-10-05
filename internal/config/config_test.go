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
	// 0 значит "не задано": пустая переменная окружения дает found=true, val="".
	// Runner сам поднимет его до минуты
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

func TestDBPathDefaults(t *testing.T) {
	def := App{ConfigDir: "/config"}
	if got := def.DBPath(); got != "/config/ripperr.db" {
		t.Errorf("default DBPath = %q, want /config/ripperr.db", got)
	}

	split := App{ConfigDir: "/config", DB: Storage{Path: "/data/x.db"}}
	if got := split.DBPath(); got != "/data/x.db" {
		t.Errorf("DBPath with DB.Path = %q, want /data/x.db", got)
	}
}

func TestSecretsDirFollowsDBPath(t *testing.T) {
	def := App{ConfigDir: "/config"}
	if got := def.SecretsDir(); got != "/config" {
		t.Errorf("default SecretsDir = %q, want /config", got)
	}

	split := App{ConfigDir: "/config", DB: Storage{Path: "/data/x.db"}}
	if got := split.SecretsDir(); got != "/data" {
		t.Errorf("SecretsDir with DB.Path = %q, want /data", got)
	}
}

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

func TestProcessDelugeEnv(t *testing.T) {
	cfg := DefaultApp()
	if err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.MapLookuper(map[string]string{}),
	}); err != nil {
		t.Fatalf("ProcessWith: %v", err)
	}
	if cfg.Manager.Deluge.Host != "localhost:8112" || cfg.Manager.Deluge.Password != "deluge" {
		t.Fatalf("defaults = %+v, want localhost:8112 / deluge", cfg.Manager.Deluge)
	}
	cfg = DefaultApp()
	if err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.MapLookuper(map[string]string{"DELUGE_URL": "dl:8112", "DELUGE_PASS": "secret"}),
	}); err != nil {
		t.Fatalf("ProcessWith: %v", err)
	}
	if cfg.Manager.Deluge.Host != "dl:8112" || cfg.Manager.Deluge.Password != "secret" {
		t.Fatalf("env = %+v, want dl:8112 / secret", cfg.Manager.Deluge)
	}
}

func TestMirrorSourceEnv(t *testing.T) {
	cfg := DefaultApp()
	if err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.MapLookuper(map[string]string{}),
	}); err != nil {
		t.Fatalf("ProcessWith: %v", err)
	}
	if want := (MirrorSource{Type: "github"}); cfg.MirrorSource != want {
		t.Fatalf("defaults = %+v, want %+v", cfg.MirrorSource, want)
	}
	cfg = DefaultApp()
	if err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.MapLookuper(map[string]string{"MIRROR_SOURCE": "jsdelivr", "MIRROR_SOURCE_DIRECT": "true"}),
	}); err != nil {
		t.Fatalf("ProcessWith: %v", err)
	}
	if want := (MirrorSource{Type: "jsdelivr", Direct: true}); cfg.MirrorSource != want {
		t.Fatalf("env = %+v, want %+v", cfg.MirrorSource, want)
	}
}

func TestWebAppURLEnv(t *testing.T) {
	cfg := DefaultApp()
	if err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.MapLookuper(map[string]string{}),
	}); err != nil {
		t.Fatalf("ProcessWith: %v", err)
	}
	if cfg.Web.AppURL != "" {
		t.Fatalf("default AppURL = %q, want empty", cfg.Web.AppURL)
	}
	cfg = DefaultApp()
	if err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.MapLookuper(map[string]string{"WEB_APP_URL": "https://r.example.com"}),
	}); err != nil {
		t.Fatalf("ProcessWith: %v", err)
	}
	if cfg.Web.AppURL != "https://r.example.com" {
		t.Fatalf("env AppURL = %q", cfg.Web.AppURL)
	}
}
