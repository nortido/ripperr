package settings

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sethvargo/go-envconfig"

	"ripperr/internal/config"
)

func envDefaultApp(t *testing.T) config.App {
	t.Helper()
	cfg := config.DefaultApp()
	if err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.MapLookuper(map[string]string{}),
	}); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestScheduleMirrorConfirm(t *testing.T) {
	cfg := envDefaultApp(t)
	if err := ApplyOverrides(context.Background(), &cfg, &fakeSettingsDB{data: map[string]json.RawMessage{}}); err != nil {
		t.Fatal(err)
	}
	if cfg.Runner.MirrorConfirm {
		t.Fatal("absent mirror_confirm: MirrorConfirm = true, want env default false")
	}

	cfg = envDefaultApp(t)
	db := &fakeSettingsDB{data: map[string]json.RawMessage{"schedule": json.RawMessage(`{"mirror_confirm":false}`)}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Runner.MirrorConfirm {
		t.Fatal(`{"mirror_confirm":false}: MirrorConfirm = true, want false`)
	}

	var doc map[string]any
	if err := json.Unmarshal(ScheduleJSON(&cfg), &doc); err != nil {
		t.Fatal(err)
	}
	if v, ok := doc["mirror_confirm"]; !ok || v != false {
		t.Fatalf("ScheduleJSON mirror_confirm = %v (present %v), want false", v, ok)
	}

	o, err := SchedulePatchFromJSON(json.RawMessage(`{"start_hour":7,"list_refresh_per_day":2,"threshold_month":1,"threshold_half_year":1,"threshold_year":1,"mirror_confirm":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if o.MirrorConfirm == nil || *o.MirrorConfirm {
		t.Fatalf("schedule patch MirrorConfirm = %v, want false", o.MirrorConfirm)
	}
}

func TestScheduleMirrorConfirmSavedTrueWinsOverDefault(t *testing.T) {
	cfg := envDefaultApp(t)
	db := &fakeSettingsDB{data: map[string]json.RawMessage{"schedule": json.RawMessage(`{"mirror_confirm":true}`)}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if !cfg.Runner.MirrorConfirm {
		t.Fatal(`{"mirror_confirm":true}: MirrorConfirm = false, want true over env default false`)
	}
}
