package settings

import (
	"context"
	"encoding/json"
	"testing"

	"trackerr/internal/config"
)

type fakeSettingsDB struct {
	data map[string]json.RawMessage
}

func (f *fakeSettingsDB) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	v, ok := f.data[key]
	return v, ok, nil
}

func (f *fakeSettingsDB) SaveSetting(_ context.Context, key string, value json.RawMessage) error {
	if f.data == nil {
		f.data = make(map[string]json.RawMessage)
	}
	f.data[key] = value
	return nil
}

func TestApplyOverridesPartial(t *testing.T) {
	cfg := config.App{
		Log:     config.Log{Level: "info"},
		Runner:  config.Runner{StartHour: 7, ListRefreshPerDay: 2, MonthThresholdDays: 1, HalfYearThresholdDays: 7, YearThresholdDays: 30, DownloadProxy: "http://env-proxy"},
		Tracker: config.Tracker{},
		Manager: config.Manager{QbitTorrent: struct {
			Host     string `env:"QBIT_URL, default=localhost:8068"`
			Username string `env:"QBIT_USER, default=admin"`
			Password string `env:"QBIT_PASS, default=adminadmin"`
		}{Host: "qbit-env", Username: "u", Password: "p"}},
		Flaresolverr: config.Flaresolverr{Host: "flare-env", MaxTimeout: 300000},
	}
	cfg.Tracker.Rutracker.Domain = "rutracker.org"
	cfg.Tracker.Rutracker.Enabled = true

	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"schedule":     json.RawMessage(`{"start_hour":9,"list_refresh_per_day":4}`),
		"trackers":     json.RawMessage(`{"rutracker":{"domain":"custom.org","enabled":false}}`),
		"integrations": json.RawMessage(`{"qbit":{"host":"qbit-db"},"proxy":{"download_proxy":"http://db-proxy"}}`),
	}}

	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Runner.StartHour != 9 {
		t.Errorf("StartHour = %d, want 9", cfg.Runner.StartHour)
	}
	if cfg.Runner.ListRefreshPerDay != 4 {
		t.Errorf("ListRefreshPerDay = %d, want 4", cfg.Runner.ListRefreshPerDay)
	}
	if cfg.Runner.MonthThresholdDays != 1 {
		t.Errorf("MonthThresholdDays should stay from env, got %d", cfg.Runner.MonthThresholdDays)
	}
	if cfg.Tracker.Rutracker.Domain != "custom.org" {
		t.Errorf("domain = %q", cfg.Tracker.Rutracker.Domain)
	}
	if cfg.Tracker.Rutracker.Enabled {
		t.Error("rutracker should be disabled from overlay")
	}
	if cfg.Manager.QbitTorrent.Host != "http://qbit-db" {
		t.Errorf("qbit host = %q", cfg.Manager.QbitTorrent.Host)
	}
	if cfg.Runner.DownloadProxy != "http://db-proxy" {
		t.Errorf("download proxy = %q", cfg.Runner.DownloadProxy)
	}
	if cfg.Flaresolverr.Host != "http://flare-env" {
		t.Errorf("flare should be normalized from env, got %q", cfg.Flaresolverr.Host)
	}
}

func TestApplyOverridesCategories(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7, AllowedCategories: []string{"env-cat"}},
		Tracker:      config.Tracker{},
		Manager:      config.Manager{DeleteFilesCategories: []string{"env-del"}},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"schedule": json.RawMessage(`{"monitored_categories":["movies"],"delete_files_categories":[]}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Runner.AllowedCategories) != 1 || cfg.Runner.AllowedCategories[0] != "movies" {
		t.Errorf("AllowedCategories = %v", cfg.Runner.AllowedCategories)
	}
	if len(cfg.Manager.DeleteFilesCategories) != 0 {
		t.Errorf("DeleteFilesCategories = %v, want empty", cfg.Manager.DeleteFilesCategories)
	}
}

func TestApplyOverridesDeleteFlags(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7},
		Tracker:      config.Tracker{},
		Manager:      config.Manager{DeleteEnabled: true, DeleteFilesEnabled: true},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"schedule": json.RawMessage(`{"delete_enabled":false,"delete_files_enabled":false}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Manager.DeleteEnabled {
		t.Error("DeleteEnabled should be false from overlay")
	}
	if cfg.Manager.DeleteFilesEnabled {
		t.Error("DeleteFilesEnabled should be false from overlay")
	}
}

func TestValidateScheduleCategories(t *testing.T) {
	s := &Schedule{
		StartHour:             7,
		ListRefreshPerDay:     2,
		ThresholdMonth:        1,
		ThresholdHalfYear:     1,
		ThresholdYear:         1,
		MonitoredCategories:   []string{" a ", "", "b"},
		DeleteFilesCategories: []string{"  ", "tv"},
	}
	if err := ValidateSchedule(s); err != nil {
		t.Fatal(err)
	}
	if len(s.MonitoredCategories) != 2 || s.MonitoredCategories[0] != "a" || s.MonitoredCategories[1] != "b" {
		t.Errorf("MonitoredCategories = %v", s.MonitoredCategories)
	}
	if len(s.DeleteFilesCategories) != 1 || s.DeleteFilesCategories[0] != "tv" {
		t.Errorf("DeleteFilesCategories = %v", s.DeleteFilesCategories)
	}
}

func TestValidateSchedule(t *testing.T) {
	if err := ValidateSchedule(&Schedule{StartHour: 25, ListRefreshPerDay: 2, ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1}); err == nil {
		t.Error("expected error for start_hour")
	}
	if err := ValidateSchedule(&Schedule{StartHour: 7, ListRefreshPerDay: 5, ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1}); err == nil {
		t.Error("expected error for list_refresh_per_day")
	}
}

func TestMergeIntegrationsPartial(t *testing.T) {
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"integrations": json.RawMessage(`{"qbit":{"host":"old","username":"u","password":"p"},"flaresolverr":{"url":"http://flare","timeout_ms":30000},"proxy":{"download_proxy":"http://proxy"}}`),
	}}
	if err := MergeIntegrations(context.Background(), db, json.RawMessage(`{"qbit":{"host":"new"}}`)); err != nil {
		t.Fatal(err)
	}
	var saved integrationsOverlay
	if err := json.Unmarshal(db.data["integrations"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Qbit.Host == nil || *saved.Qbit.Host != "http://new" {
		t.Errorf("qbit host = %v, want http://new", saved.Qbit.Host)
	}
	if saved.Qbit.Username == nil || *saved.Qbit.Username != "u" {
		t.Errorf("qbit username = %v, want u", saved.Qbit.Username)
	}
	if saved.Flaresolverr.URL == nil || *saved.Flaresolverr.URL != "http://flare" {
		t.Errorf("flaresolverr url = %v", saved.Flaresolverr.URL)
	}
	if saved.Proxy.DownloadProxy == nil || *saved.Proxy.DownloadProxy != "http://proxy" {
		t.Errorf("proxy = %v", saved.Proxy.DownloadProxy)
	}
}

func TestTrackerPatchFromJSONRejectsStringHourLimit(t *testing.T) {
	_, err := TrackerPatchFromJSON(json.RawMessage(`{"hour_limit":"20"}`))
	if err == nil {
		t.Fatal("expected type error")
	}
}

func TestMergeTrackerMirrors(t *testing.T) {
	db := &fakeSettingsDB{}
	if err := MergeTrackerKey(context.Background(), db, "rutracker", json.RawMessage(`{"mirrors":[" mirror.org ","","backup.net"]}`)); err != nil {
		t.Fatal(err)
	}
	var saved map[string]trackerItemOverlay
	if err := json.Unmarshal(db.data["trackers"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved["rutracker"].Mirrors == nil || len(*saved["rutracker"].Mirrors) != 2 {
		t.Fatalf("mirrors = %v", saved["rutracker"].Mirrors)
	}
}

func TestValidateTrackerMirrors(t *testing.T) {
	_, err := TrackerPatchFromJSON(json.RawMessage(`{"mirrors":["bad domain"]}`))
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestApplyOverridesWatchTagsTimezone(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7, WatchTags: []string{"watch"}},
		Tracker:      config.Tracker{},
		Manager:      config.Manager{},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"schedule": json.RawMessage(`{"watch_tags":["custom","track"],"timezone":"Europe/Moscow"}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Runner.WatchTags) != 2 || cfg.Runner.WatchTags[0] != "custom" || cfg.Runner.WatchTags[1] != "track" {
		t.Errorf("WatchTags = %v", cfg.Runner.WatchTags)
	}
	if cfg.Runner.Timezone != "Europe/Moscow" {
		t.Errorf("Timezone = %q", cfg.Runner.Timezone)
	}
}

func TestApplyOverridesLegacyWatchTag(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7, WatchTags: []string{"watch"}},
		Tracker:      config.Tracker{},
		Manager:      config.Manager{},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"schedule": json.RawMessage(`{"watch_tag":"legacy"}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Runner.WatchTags) != 1 || cfg.Runner.WatchTags[0] != "legacy" {
		t.Errorf("WatchTags = %v, want [legacy]", cfg.Runner.WatchTags)
	}
}

func TestSchedulePatchLegacyWatchTagConvertsToWatchTags(t *testing.T) {
	o, err := SchedulePatchFromJSON(json.RawMessage(`{
		"start_hour":7,"list_refresh_per_day":2,
		"threshold_month":1,"threshold_half_year":1,"threshold_year":1,
		"watch_tag":"legacy"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if o.WatchTags == nil || len(*o.WatchTags) != 1 || (*o.WatchTags)[0] != "legacy" {
		t.Fatalf("WatchTags = %v, want [legacy]", o.WatchTags)
	}
	if o.WatchTag != nil {
		t.Fatalf("legacy WatchTag should be cleared, got %q", *o.WatchTag)
	}
}

func TestSchedulePatchWatchTagsEmptyAllowed(t *testing.T) {
	o, err := SchedulePatchFromJSON(json.RawMessage(`{
		"start_hour":7,"list_refresh_per_day":2,
		"threshold_month":1,"threshold_half_year":1,"threshold_year":1,
		"watch_tags":[]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if o.WatchTags == nil || len(*o.WatchTags) != 0 {
		t.Fatalf("WatchTags = %v, want []", o.WatchTags)
	}
}

func TestSchedulePatchWatchTagsRejectsComma(t *testing.T) {
	_, err := SchedulePatchFromJSON(json.RawMessage(`{
		"start_hour":7,"list_refresh_per_day":2,
		"threshold_month":1,"threshold_half_year":1,"threshold_year":1,
		"watch_tags":["bad,tag"]
	}`))
	if err == nil {
		t.Fatal("expected validation error")
	}
}
