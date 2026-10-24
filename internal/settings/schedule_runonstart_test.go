package settings

import (
	"context"
	"encoding/json"
	"testing"
)

func TestScheduleRunOnStart(t *testing.T) {
	cfg := envDefaultApp(t)
	if err := ApplyOverrides(context.Background(), &cfg, &fakeSettingsDB{data: map[string]json.RawMessage{}}); err != nil {
		t.Fatal(err)
	}
	if !cfg.Runner.RunOnStart {
		t.Fatal("absent run_on_start: RunOnStart = false, want env default true")
	}

	cfg = envDefaultApp(t)
	db := &fakeSettingsDB{data: map[string]json.RawMessage{"schedule": json.RawMessage(`{"run_on_start":false}`)}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Runner.RunOnStart {
		t.Fatal(`{"run_on_start":false}: RunOnStart = true, want false`)
	}

	var doc map[string]any
	if err := json.Unmarshal(ScheduleJSON(&cfg), &doc); err != nil {
		t.Fatal(err)
	}
	if v, ok := doc["run_on_start"]; !ok || v != false {
		t.Fatalf("ScheduleJSON run_on_start = %v (present %v), want false", v, ok)
	}

	o, err := SchedulePatchFromJSON(json.RawMessage(`{"start_hour":7,"list_refresh_per_day":2,"threshold_month":1,"threshold_half_year":1,"threshold_year":1,"run_on_start":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if o.RunOnStart == nil || *o.RunOnStart {
		t.Fatalf("schedule patch RunOnStart = %v, want false", o.RunOnStart)
	}
}
