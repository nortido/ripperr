package settings

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/notify"
	"ripperr/internal/system/logger"
)

type fakeSettingsDB struct {
	data  map[string]json.RawMessage
	saves int
}

func (f *fakeSettingsDB) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	v, ok := f.data[key]
	return v, ok, nil
}

func (f *fakeSettingsDB) SaveSetting(_ context.Context, key string, value json.RawMessage) error {
	f.saves++
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
	cfg.Tracker["rutracker"] = &config.TrackerItem{Name: "rutracker", Domain: "rutracker.org", Enabled: true}

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
	if cfg.Tracker["rutracker"].Domain != "custom.org" {
		t.Errorf("domain = %q", cfg.Tracker["rutracker"].Domain)
	}
	if cfg.Tracker["rutracker"].Enabled {
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
		MagnetTimeout:         60,
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

func TestApplyOverridesClientAndTransmission(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7},
		Manager:      config.Manager{Client: "qbit"},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"integrations": json.RawMessage(`{"client":"transmission","transmission":{"host":"tm-host","username":"u","password":"p"}}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Manager.Client != "transmission" {
		t.Errorf("client = %q, want transmission", cfg.Manager.Client)
	}
	if cfg.Manager.Transmission.Host != "http://tm-host" {
		t.Errorf("transmission host = %q, want http://tm-host (normalized)", cfg.Manager.Transmission.Host)
	}
	if cfg.Manager.Transmission.Username != "u" || cfg.Manager.Transmission.Password != "p" {
		t.Errorf("transmission creds = %q/%q, want u/p", cfg.Manager.Transmission.Username, cfg.Manager.Transmission.Password)
	}
}

func TestApplyOverridesUnknownClientIgnored(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7},
		Manager:      config.Manager{Client: "qbit"},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"integrations": json.RawMessage(`{"client":"utorrent"}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Manager.Client != "qbit" {
		t.Errorf("client = %q, want qbit (unknown value ignored)", cfg.Manager.Client)
	}
}

func TestApplyOverridesEmptyClientPatchKeepsExisting(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7},
		Manager:      config.Manager{Client: "transmission"},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"integrations": json.RawMessage(`{"client":""}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Manager.Client != "transmission" {
		t.Errorf("client = %q, want transmission (empty patch value keeps active client)", cfg.Manager.Client)
	}
}

func TestApplyOverridesOldJSONWithoutClientKeepsQbit(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7},
		Manager:      config.Manager{Client: "qbit"},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"integrations": json.RawMessage(`{"qbit":{"host":"old-host"}}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Manager.Client != "qbit" {
		t.Errorf("client = %q, want qbit", cfg.Manager.Client)
	}
	if cfg.Manager.QbitTorrent.Host != "http://old-host" {
		t.Errorf("qbit host = %q", cfg.Manager.QbitTorrent.Host)
	}
}

func TestApplyOverridesEnvMixedCaseClientNormalized(t *testing.T) {
	// MANAGER_CLIENT=Transmission из env должен дойти до cfg.Manager.Client в каноническом виде
	// и без оверлея из БД, иначе уйдет в ветку qbit за учетными данными
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7},
		Manager:      config.Manager{Client: " Transmission "},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Manager.Client != "transmission" {
		t.Errorf("client = %q, want transmission (normalized)", cfg.Manager.Client)
	}
}

func TestApplyOverridesPatchMixedCaseClientApplied(t *testing.T) {
	// UI может прислать "Transmission" с большой буквы. Такой патч должен пройти валидацию
	// и включить transmission, а не остатся на qbit
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7},
		Manager:      config.Manager{Client: "qbit"},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"integrations": json.RawMessage(`{"client":"Transmission"}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Manager.Client != "transmission" {
		t.Errorf("client = %q, want transmission", cfg.Manager.Client)
	}
}

func TestIntegrationsJSONIncludesClientAndTransmission(t *testing.T) {
	cfg := &config.App{
		Manager: config.Manager{Client: "transmission"},
	}
	cfg.Manager.Transmission.Host = "http://tm-host"
	cfg.Manager.Transmission.Username = "u"
	cfg.Manager.Transmission.Password = "p"

	var got struct {
		Client       string `json:"client"`
		Transmission struct {
			Host     string `json:"host"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"transmission"`
	}
	if err := json.Unmarshal(IntegrationsJSON(cfg), &got); err != nil {
		t.Fatal(err)
	}
	if got.Client != "transmission" {
		t.Errorf("client = %q, want transmission", got.Client)
	}
	if got.Transmission.Host != "http://tm-host" || got.Transmission.Username != "u" || got.Transmission.Password != SecretMask {
		t.Errorf("transmission = %+v", got.Transmission)
	}
}

func TestIntegrationsJSONNormalizesRawEnvCasing(t *testing.T) {
	// Client тут такой, как его кладет env-парсер из MANAGER_CLIENT=Transmission, без ApplyOverrides.
	// GET /api/integrations должен отдать "transmission", иначе в селекте UI ничего не выбрано
	cfg := &config.App{
		Manager: config.Manager{Client: "Transmission"},
	}
	var got struct {
		Client string `json:"client"`
	}
	if err := json.Unmarshal(IntegrationsJSON(cfg), &got); err != nil {
		t.Fatal(err)
	}
	if got.Client != "transmission" {
		t.Errorf("client = %q, want transmission", got.Client)
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

func TestApplyOverridesMagnetTimeoutOverridesEnv(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7, MagnetTimeout: 60},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"schedule": json.RawMessage(`{"magnet_timeout":120}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Runner.MagnetTimeout != 120 {
		t.Errorf("MagnetTimeout = %d, want 120", cfg.Runner.MagnetTimeout)
	}
}

func TestApplyOverridesMagnetTimeoutAbsentKeepsEnv(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7, MagnetTimeout: 60},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"schedule": json.RawMessage(`{"start_hour":9}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Runner.MagnetTimeout != 60 {
		t.Errorf("MagnetTimeout = %d, want 60 (from env, key absent)", cfg.Runner.MagnetTimeout)
	}
}

func TestScheduleJSONIncludesMagnetTimeout(t *testing.T) {
	cfg := &config.App{
		Runner: config.Runner{MagnetTimeout: 90},
	}
	var got struct {
		MagnetTimeout int64 `json:"magnet_timeout"`
	}
	if err := json.Unmarshal(ScheduleJSON(cfg), &got); err != nil {
		t.Fatal(err)
	}
	if got.MagnetTimeout != 90 {
		t.Errorf("magnet_timeout = %d, want 90", got.MagnetTimeout)
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

func TestApplyOverridesDoesNotMutateBaseTrackers(t *testing.T) {
	base := config.App{Tracker: config.Tracker{
		"rutracker": &config.TrackerItem{Name: "rutracker", Domain: "rutracker.org", Enabled: true},
	}}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"domain":"custom.org","enabled":false}}`),
	}}

	cfg := base
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Tracker["rutracker"].Domain != "custom.org" {
		t.Fatalf("overlay domain = %q, want custom.org", cfg.Tracker["rutracker"].Domain)
	}
	if base.Tracker["rutracker"].Domain != "rutracker.org" {
		t.Fatalf("base domain = %q, want untouched rutracker.org", base.Tracker["rutracker"].Domain)
	}
	if !base.Tracker["rutracker"].Enabled {
		t.Fatal("base Enabled flipped to false")
	}
}

func TestMergeIntegrationsEventsNilVsEmpty(t *testing.T) {
	db := &fakeSettingsDB{}
	ctx := context.Background()
	if err := MergeIntegrations(ctx, db, json.RawMessage(`{"notifications":{"webhook":{"url":"http://h"}}}`)); err != nil {
		t.Fatal(err)
	}
	var saved integrationsOverlay
	if err := json.Unmarshal(db.data["integrations"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Notifications.Webhook.Events != nil || saved.Notifications.Telegram.Events != nil {
		t.Fatalf("absent events must stay absent: %s", db.data["integrations"])
	}

	if err := MergeIntegrations(ctx, db, json.RawMessage(`{"notifications":{"webhook":{"events":[]},"telegram":{"events":["updated","updated","check_failed"]}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(db.data["integrations"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Notifications.Webhook.Events == nil || len(*saved.Notifications.Webhook.Events) != 0 {
		t.Fatalf("empty list must persist: %s", db.data["integrations"])
	}
	if got := saved.Notifications.Telegram.Events; got == nil || len(*got) != 2 || (*got)[0] != "updated" || (*got)[1] != "check_failed" {
		t.Fatalf("telegram events not deduped: %s", db.data["integrations"])
	}

	if err := MergeIntegrations(ctx, db, json.RawMessage(`{"notifications":{"enabled":true}}`)); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(db.data["integrations"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Notifications.Webhook.Events == nil || saved.Notifications.Telegram.Events == nil {
		t.Fatalf("patch without events must keep stored lists: %s", db.data["integrations"])
	}

	cfg := &config.App{}
	applyIntegrations(cfg, saved)
	if !slices.Contains(cfg.Notify.Events, "check_failed") {
		t.Fatalf("shared set must be the union of stored channel lists: %v", cfg.Notify.Events)
	}
}

func TestIntegrationsJSONShowsResolvedEvents(t *testing.T) {
	cfg := &config.App{}
	var got struct {
		Notifications struct {
			Events []string `json:"events"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(IntegrationsJSON(cfg), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Notifications.Events) != len(notify.DefaultEvents()) {
		t.Fatalf("nil list must resolve to defaults: %+v", got.Notifications)
	}
	cfg.Notify.Events = []string{}
	if err := json.Unmarshal(IntegrationsJSON(cfg), &got); err != nil {
		t.Fatal(err)
	}
	if got.Notifications.Events == nil || len(got.Notifications.Events) != 0 {
		t.Fatalf("empty list must stay [] in JSON, got %#v", got.Notifications.Events)
	}
	cfg.Notify.Events = []string{"check_failed"}
	if err := json.Unmarshal(IntegrationsJSON(cfg), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Notifications.Events) != 1 || got.Notifications.Events[0] != "check_failed" {
		t.Fatalf("events = %#v", got.Notifications.Events)
	}
}

func TestIntegrationsJSONIncludesEmailPushover(t *testing.T) {
	cfg := &config.App{}
	cfg.Notify.EmailHost = "smtp.example.com"
	cfg.Notify.EmailPort = 465
	cfg.Notify.EmailSecurity = "tls"
	cfg.Notify.EmailUsername = "user"
	cfg.Notify.EmailPassword = "pw"
	cfg.Notify.EmailFrom = "bot@example.com"
	cfg.Notify.EmailTo = "a@example.com"
	cfg.Notify.PushoverToken = "apptok"
	cfg.Notify.PushoverUser = "userkey"
	var got struct {
		Notifications struct {
			Email struct {
				Host     string `json:"host"`
				Port     int    `json:"port"`
				Security string `json:"security"`
				Username string `json:"username"`
				Password string `json:"password"`
				From     string `json:"from"`
				To       string `json:"to"`
			} `json:"email"`
			Pushover struct {
				AppToken string `json:"app_token"`
				UserKey  string `json:"user_key"`
			} `json:"pushover"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(IntegrationsJSON(cfg), &got); err != nil {
		t.Fatal(err)
	}
	e, p := got.Notifications.Email, got.Notifications.Pushover
	if e.Host != "smtp.example.com" || e.Port != 465 || e.Security != "tls" || e.Username != "user" || e.Password != SecretMask || e.From != "bot@example.com" || e.To != "a@example.com" {
		t.Fatalf("email = %+v", e)
	}
	if p.AppToken != SecretMask || p.UserKey != SecretMask {
		t.Fatalf("pushover = %+v", p)
	}

	db := &fakeSettingsDB{}
	body := `{"notifications":{"email":{"host":"mx","port":587,"security":"starttls","username":"u","password":"p","from":"f@x.example","to":"t@x.example","events":["check_failed"]},"pushover":{"app_token":"a","user_key":"k","events":[]}}}`
	if err := MergeIntegrations(context.Background(), db, json.RawMessage(body)); err != nil {
		t.Fatal(err)
	}
	var saved integrationsOverlay
	if err := json.Unmarshal(db.data["integrations"], &saved); err != nil {
		t.Fatal(err)
	}
	out := &config.App{}
	applyIntegrations(out, saved)
	n := out.Notify
	if n.EmailHost != "mx" || n.EmailPort != 587 || n.EmailSecurity != "starttls" || n.EmailUsername != "u" || n.EmailPassword != "p" || n.EmailFrom != "f@x.example" || n.EmailTo != "t@x.example" {
		t.Fatalf("applied email = %+v", n)
	}
	if n.PushoverToken != "a" || n.PushoverUser != "k" {
		t.Fatalf("applied = %+v", n)
	}
}

func TestMetricsTokenRoundTrip(t *testing.T) {
	db := &fakeSettingsDB{}
	if err := MergeIntegrations(context.Background(), db, json.RawMessage(`{"metrics":{"token":"s3cret"}}`)); err != nil {
		t.Fatal(err)
	}
	var cfg config.App
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Metrics.Token != "s3cret" {
		t.Fatalf("applied token = %q", cfg.Metrics.Token)
	}
	var got struct {
		Metrics struct {
			Token string `json:"token"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(IntegrationsJSON(&cfg), &got); err != nil {
		t.Fatal(err)
	}
	if got.Metrics.Token != SecretMask {
		t.Fatalf("json token = %q", got.Metrics.Token)
	}

	if err := MergeIntegrations(context.Background(), db, json.RawMessage(`{"proxy":{"download_proxy":""}}`)); err != nil {
		t.Fatal(err)
	}
	cfg = config.App{}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Metrics.Token != "s3cret" {
		t.Fatalf("unrelated patch must keep token, got %q", cfg.Metrics.Token)
	}
	if err := MergeIntegrations(context.Background(), db, json.RawMessage(`{"metrics":{"token":""}}`)); err != nil {
		t.Fatal(err)
	}
	cfg = config.App{}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Metrics.Token != "" {
		t.Fatalf("cleared token = %q", cfg.Metrics.Token)
	}
}

func TestApplyOverridesClientDeluge(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7},
		Manager:      config.Manager{Client: "qbit"},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	cfg.Manager.QbitTorrent.Host = "http://qb-host"
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"integrations": json.RawMessage(`{"client":"Deluge","deluge":{"host":"dl-host:8112","password":"p"}}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Manager.Client != "deluge" {
		t.Errorf("client = %q, want deluge", cfg.Manager.Client)
	}
	if cfg.Manager.Deluge.Host != "http://dl-host:8112" || cfg.Manager.Deluge.Password != "p" {
		t.Errorf("deluge = %+v, want http://dl-host:8112 / p", cfg.Manager.Deluge)
	}
	if cfg.Manager.QbitTorrent.Host != "http://qb-host" {
		t.Errorf("qbit host = %q, switching client must keep other client settings", cfg.Manager.QbitTorrent.Host)
	}
}

func TestApplyOverridesEnvDelugeNormalized(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7},
		Manager:      config.Manager{Client: " Deluge "},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	cfg.Manager.Deluge.Host = "localhost:8112"
	if err := ApplyOverrides(context.Background(), &cfg, &fakeSettingsDB{}); err != nil {
		t.Fatal(err)
	}
	if cfg.Manager.Client != "deluge" {
		t.Errorf("client = %q, want deluge", cfg.Manager.Client)
	}
	if cfg.Manager.Deluge.Host != "http://localhost:8112" {
		t.Errorf("deluge host = %q, want http://localhost:8112", cfg.Manager.Deluge.Host)
	}
}

func TestIntegrationsJSONIncludesDeluge(t *testing.T) {
	cfg := &config.App{Manager: config.Manager{Client: "Deluge"}}
	cfg.Manager.Deluge.Host = "http://dl-host:8112"
	cfg.Manager.Deluge.Password = "p"
	var got struct {
		Client string `json:"client"`
		Deluge struct {
			Host     string `json:"host"`
			Password string `json:"password"`
		} `json:"deluge"`
	}
	if err := json.Unmarshal(IntegrationsJSON(cfg), &got); err != nil {
		t.Fatal(err)
	}
	if got.Client != "deluge" {
		t.Errorf("client = %q, want deluge", got.Client)
	}
	if got.Deluge.Host != "http://dl-host:8112" || got.Deluge.Password != SecretMask {
		t.Errorf("deluge = %+v", got.Deluge)
	}
}

func TestApplyOverridesWebSection(t *testing.T) {
	const doc = `{"port":7000,"allowed_hosts":["my.keenetic.pro"]}`
	cases := []struct {
		name       string
		data       map[string]json.RawMessage
		listen     string
		wantListen string
		wantHosts  []string
		locked     bool
	}{
		{"port only host", map[string]json.RawMessage{"web": json.RawMessage(doc)}, ":6410", ":7000", []string{"my.keenetic.pro"}, false},
		{"port keeps host part", map[string]json.RawMessage{"web": json.RawMessage(doc)}, "127.0.0.1:6410", "127.0.0.1:7000", []string{"my.keenetic.pro"}, false},
		{"no web row", map[string]json.RawMessage{}, ":6410", ":6410", []string{"a.example"}, false},
		{"invalid json", map[string]json.RawMessage{"web": json.RawMessage(`{"port":`)}, ":6410", ":6410", []string{"a.example"}, false},
		{"locked ignores stored port", map[string]json.RawMessage{"web": json.RawMessage(doc)}, ":6410", ":6410", []string{"my.keenetic.pro"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.App{Web: config.Web{Listen: tc.listen, PortLocked: tc.locked, AllowedHosts: []string{"a.example"}}}
			if err := ApplyOverrides(context.Background(), &cfg, &fakeSettingsDB{data: tc.data}); err != nil {
				t.Fatal(err)
			}
			if cfg.Web.Listen != tc.wantListen {
				t.Errorf("Listen = %q, want %q", cfg.Web.Listen, tc.wantListen)
			}
			if !slices.Equal(cfg.Web.AllowedHosts, tc.wantHosts) {
				t.Errorf("AllowedHosts = %v, want %v", cfg.Web.AllowedHosts, tc.wantHosts)
			}
		})
	}
}

func TestWebJSON(t *testing.T) {
	cfg := &config.App{Web: config.Web{Listen: "127.0.0.1:6410"}}
	if got := string(WebJSON(cfg)); got != `{"allowed_hosts":[],"port":6410}` {
		t.Errorf("WebJSON = %s", got)
	}
	cfg.Web.AllowedHosts = []string{"x.lan"}
	if got := string(WebJSON(cfg)); got != `{"allowed_hosts":["x.lan"],"port":6410}` {
		t.Errorf("WebJSON = %s", got)
	}
}

func TestMergeWebKeepsOtherField(t *testing.T) {
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"web": json.RawMessage(`{"port":7000,"allowed_hosts":["x.lan"]}`),
	}}
	if err := MergeWeb(context.Background(), db, json.RawMessage(`{"port":7001}`)); err != nil {
		t.Fatal(err)
	}
	var saved webOverlay
	if err := json.Unmarshal(db.data["web"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Port == nil || *saved.Port != 7001 || saved.AllowedHosts == nil || !slices.Equal(*saved.AllowedHosts, []string{"x.lan"}) {
		t.Errorf("after port patch: %s", db.data["web"])
	}
	if err := MergeWeb(context.Background(), db, json.RawMessage(`{"allowed_hosts":["Y.lan"]}`)); err != nil {
		t.Fatal(err)
	}
	saved = webOverlay{}
	if err := json.Unmarshal(db.data["web"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Port == nil || *saved.Port != 7001 || saved.AllowedHosts == nil || !slices.Equal(*saved.AllowedHosts, []string{"y.lan"}) {
		t.Errorf("after hosts patch: %s", db.data["web"])
	}
}

func TestMergeTrackerKeyLoginPassword(t *testing.T) {
	ctx := context.Background()
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"password":"SEC-p"}}`),
	}}
	if err := MergeTrackerKey(ctx, db, "rutracker", json.RawMessage(`{"login":"a"}`)); err != nil {
		t.Fatal(err)
	}
	var saved map[string]map[string]any
	if err := json.Unmarshal(db.data["trackers"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved["rutracker"]["login"] != "a" || saved["rutracker"]["password"] != "SEC-p" {
		t.Fatalf("saved = %s", db.data["trackers"])
	}
	cfg := config.App{Tracker: config.Tracker{"rutracker": {Name: "rutracker"}}}
	eff, err := TrackersEffective(ctx, &cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if it := eff["rutracker"]; it.Login != "a" || it.Password != "SEC-p" {
		t.Fatalf("effective login %q password %q", it.Login, it.Password)
	}
}

func TestApplyOverridesDisablesTrackersNotAdded(t *testing.T) {
	cases := []struct {
		name          string
		data          map[string]json.RawMessage
		wantRutracker bool
		wantWarn      bool
	}{
		{"added list", map[string]json.RawMessage{"trackers_added": json.RawMessage(`["kinozal"]`)}, false, false},
		{"no list", map[string]json.RawMessage{}, true, false},
		{"invalid list", map[string]json.RawMessage{"trackers_added": json.RawMessage(`{"x":1}`)}, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.App{Tracker: config.Tracker{
				"rutracker": {Name: "rutracker", Enabled: true},
				"kinozal":   {Name: "kinozal", Enabled: true},
			}}
			if err := ApplyOverrides(context.Background(), &cfg, &fakeSettingsDB{data: tc.data}); err != nil {
				t.Fatal(err)
			}
			if cfg.Tracker["rutracker"].Enabled != tc.wantRutracker {
				t.Errorf("rutracker Enabled = %v, want %v", cfg.Tracker["rutracker"].Enabled, tc.wantRutracker)
			}
			if !cfg.Tracker["kinozal"].Enabled {
				t.Error("kinozal should stay enabled")
			}
			if tc.wantWarn && !hasWarn("settings trackers_added") {
				t.Error("no WARN naming trackers_added")
			}
		})
	}
}

func hasWarn(needle string) bool {
	for _, e := range logger.Snapshot(0) {
		if e.Level == "WARN" && strings.Contains(e.Message, needle) {
			return true
		}
	}
	return false
}

func TestTrackersAddedRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := &fakeSettingsDB{}
	if keys, ok, err := TrackersAdded(ctx, db); err != nil || ok || keys != nil {
		t.Fatalf("absent: keys %v ok %v err %v", keys, ok, err)
	}
	if err := SaveTrackersAdded(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	if got := string(db.data["trackers_added"]); got != "[]" {
		t.Fatalf("stored %q, want []", got)
	}
	if keys, ok, err := TrackersAdded(ctx, db); err != nil || !ok || keys == nil || len(keys) != 0 {
		t.Fatalf("empty: keys %#v ok %v err %v", keys, ok, err)
	}
	if err := SaveTrackersAdded(ctx, db, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if keys, ok, err := TrackersAdded(ctx, db); err != nil || !ok || !slices.Equal(keys, []string{"a", "b"}) {
		t.Fatalf("two: keys %v ok %v err %v", keys, ok, err)
	}
}

func TestDeleteTrackerKey(t *testing.T) {
	ctx := context.Background()
	const ru = `{"credentials":"SEC-c","login":"u","password":"SEC-p","x_unknown":1}`
	doc := `{"rutracker":` + ru + `,"kinozal":{"login":"k","password":"SEC-k"}}`
	db := &fakeSettingsDB{data: map[string]json.RawMessage{"trackers": json.RawMessage(doc)}}
	if err := DeleteTrackerKey(ctx, db, "kinozal"); err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(db.data["trackers"], &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || string(saved["rutracker"]) != ru {
		t.Fatalf("saved = %s", db.data["trackers"])
	}

	db.saves = 0
	if err := DeleteTrackerKey(ctx, db, "kinozal"); err != nil || db.saves != 0 {
		t.Fatalf("absent key: err %v saves %d", err, db.saves)
	}

	empty := &fakeSettingsDB{}
	if err := DeleteTrackerKey(ctx, empty, "kinozal"); err != nil || empty.saves != 0 {
		t.Fatalf("absent document: err %v saves %d", err, empty.saves)
	}

	bad := &fakeSettingsDB{data: map[string]json.RawMessage{"trackers": json.RawMessage(`{"rutracker":`)}}
	if err := DeleteTrackerKey(ctx, bad, "rutracker"); err == nil || bad.saves != 0 {
		t.Fatalf("invalid JSON: err %v saves %d", err, bad.saves)
	}
}

func channelEnabledFlags(t *testing.T, cfg *config.App) map[string]bool {
	t.Helper()
	var got struct {
		Notifications map[string]json.RawMessage `json:"notifications"`
	}
	if err := json.Unmarshal(IntegrationsJSON(cfg), &got); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, ch := range []string{"telegram", "webhook", "email", "pushover"} {
		var c struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.Unmarshal(got.Notifications[ch], &c); err != nil {
			t.Fatal(err)
		}
		if c.Enabled == nil {
			t.Fatalf("%s: no enabled key in IntegrationsJSON", ch)
		}
		out[ch] = *c.Enabled
	}
	return out
}

func TestIntegrationsChannelEnabledRoundTrip(t *testing.T) {
	db := &fakeSettingsDB{}
	if err := MergeIntegrations(context.Background(), db, json.RawMessage(`{"notifications":{"email":{"enabled":false}}}`)); err != nil {
		t.Fatal(err)
	}
	cfg := config.App{}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	n := cfg.Notify
	if !n.EmailDisabled || n.TelegramDisabled || n.WebhookDisabled || n.PushoverDisabled {
		t.Fatalf("disabled flags = tg %v wh %v email %v po %v, want only email", n.TelegramDisabled, n.WebhookDisabled, n.EmailDisabled, n.PushoverDisabled)
	}
	want := map[string]bool{"telegram": true, "webhook": true, "email": false, "pushover": true}
	if got := channelEnabledFlags(t, &cfg); !maps.Equal(got, want) {
		t.Fatalf("enabled in JSON = %v, want %v", got, want)
	}

	if err := MergeIntegrations(context.Background(), db, json.RawMessage(`{"notifications":{"email":{"enabled":true},"telegram":{"enabled":false}}}`)); err != nil {
		t.Fatal(err)
	}
	cfg = config.App{}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	want = map[string]bool{"telegram": false, "webhook": true, "email": true, "pushover": true}
	if got := channelEnabledFlags(t, &cfg); !maps.Equal(got, want) {
		t.Fatalf("enabled in JSON after second patch = %v, want %v", got, want)
	}
}

func TestIntegrationsChannelEnabledAbsentMeansOn(t *testing.T) {
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"integrations": json.RawMessage(`{"notifications":{"telegram":{"bot_token":"t","chat_id":"1"},"webhook":{"url":"http://h"},"enabled":true}}`),
	}}
	cfg := config.App{}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	n := cfg.Notify
	if n.TelegramDisabled || n.WebhookDisabled || n.EmailDisabled || n.PushoverDisabled {
		t.Fatalf("absent flag must mean enabled: %+v", n)
	}
	want := map[string]bool{"telegram": true, "webhook": true, "email": true, "pushover": true}
	if got := channelEnabledFlags(t, &cfg); !maps.Equal(got, want) {
		t.Fatalf("enabled in JSON = %v, want %v", got, want)
	}
}

func TestNormalizeAppURL(t *testing.T) {
	ok := map[string]string{
		"":                                "",
		"  ":                              "",
		"https://rip.example.com":         "https://rip.example.com",
		"https://rip.example.com/":        "https://rip.example.com",
		" HTTP://Rip.Example.com:6410// ": "http://rip.example.com:6410",
		"https://example.com/ripperr/":    "https://example.com/ripperr",
		"http://192.168.1.5:6410":         "http://192.168.1.5:6410",
		"http://[::1]:6410/app":           "http://[::1]:6410/app",
	}
	for in, want := range ok {
		got, err := normalizeAppURL(in)
		if err != nil || got != want {
			t.Errorf("normalizeAppURL(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	bad := []string{
		"rip.example.com",
		"ftp://rip.example.com",
		"https://",
		"https:///app",
		"https://u:p@rip.example.com",
		"https://u@rip.example.com",
		"https://rip.example.com?x=1",
		"https://rip.example.com/?",
		"https://rip.example.com/#a",
		"https://rip.example.com#",
		"https://rip.example.com:abc",
		"https://rip example.com",
		"//rip.example.com",
		"javascript:alert(1)",
	}
	for _, in := range bad {
		if got, err := normalizeAppURL(in); err == nil || !strings.Contains(err.Error(), "app_url") {
			t.Errorf("normalizeAppURL(%q) = %q, %v, want app_url error", in, got, err)
		}
	}
}

func TestWebPatchFromJSONAppURL(t *testing.T) {
	o, err := WebPatchFromJSON(json.RawMessage(`{"app_url":"https://x.example/"}`))
	if err != nil || o.AppURL == nil || *o.AppURL != "https://x.example" {
		t.Fatalf("patch = %+v, %v", o, err)
	}
	if _, err = WebPatchFromJSON(json.RawMessage(`{"app_url":"ftp://x"}`)); err == nil {
		t.Fatal("ftp scheme accepted")
	}
	if o, err = WebPatchFromJSON(json.RawMessage(`{"port":7000}`)); err != nil || o.AppURL != nil {
		t.Fatalf("absent app_url must stay nil: %+v, %v", o, err)
	}
}

func TestAppURLRoundTrip(t *testing.T) {
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"web": json.RawMessage(`{"port":7000,"allowed_hosts":["x.lan"]}`),
	}}
	if err := MergeWeb(context.Background(), db, json.RawMessage(`{"app_url":"https://Rip.example.com/app/"}`)); err != nil {
		t.Fatal(err)
	}
	var saved webOverlay
	if err := json.Unmarshal(db.data["web"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved.AppURL == nil || *saved.AppURL != "https://rip.example.com/app" || saved.Port == nil || *saved.Port != 7000 {
		t.Fatalf("saved = %s", db.data["web"])
	}
	cfg := config.App{Web: config.Web{Listen: ":6410", AppURL: "https://env.example.com"}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Web.AppURL != "https://rip.example.com/app" {
		t.Errorf("stored app_url must win over env: %q", cfg.Web.AppURL)
	}
	if err := MergeWeb(context.Background(), db, json.RawMessage(`{"allowed_hosts":["y.lan"]}`)); err != nil {
		t.Fatal(err)
	}
	cfg = config.App{Web: config.Web{Listen: ":6410"}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Web.AppURL != "https://rip.example.com/app" {
		t.Errorf("patch without app_url must keep it: %q", cfg.Web.AppURL)
	}
	// пустая строка очищает сохраненное значение, env-дефолт тогда тоже перекрыт
	if err := MergeWeb(context.Background(), db, json.RawMessage(`{"app_url":""}`)); err != nil {
		t.Fatal(err)
	}
	cfg = config.App{Web: config.Web{Listen: ":6410", AppURL: "https://env.example.com"}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Web.AppURL != "" {
		t.Errorf("cleared app_url must override env: %q", cfg.Web.AppURL)
	}
}

func TestApplyOverridesNormalizesEnvAppURL(t *testing.T) {
	cases := map[string]string{
		"https://h.example/":        "https://h.example",
		" HTTPS://H.example/app// ": "https://h.example/app",
		"h.example":                 "",
		"https://u:p@h.example":     "",
		"":                          "",
	}
	for in, want := range cases {
		cfg := config.App{Web: config.Web{Listen: ":6410", AppURL: in}}
		if err := ApplyOverrides(context.Background(), &cfg, &fakeSettingsDB{}); err != nil {
			t.Fatal(err)
		}
		if cfg.Web.AppURL != want {
			t.Errorf("env %q -> %q, want %q", in, cfg.Web.AppURL, want)
		}
	}
}

func countWarns(sub string) int {
	n := 0
	for _, e := range logger.Snapshot(1000) {
		if e.Level == "WARN" && strings.Contains(e.Message, sub) {
			n++
		}
	}
	return n
}

func TestInvalidAppURLWarnsOncePerValue(t *testing.T) {
	resetWarned()
	const marker = "ftp://warn-once-test.invalid"
	base, base2 := countWarns(marker), countWarns(marker+"2")
	for range 3 {
		cfg := config.App{Web: config.Web{Listen: ":6410", AppURL: marker}}
		if err := ApplyOverrides(context.Background(), &cfg, &fakeSettingsDB{}); err != nil {
			t.Fatal(err)
		}
		if cfg.Web.AppURL != "" {
			t.Fatalf("AppURL = %q, want cleared", cfg.Web.AppURL)
		}
	}
	if got := countWarns(marker) - base; got != 1 {
		t.Errorf("warnings = %d, want 1", got)
	}
	cfg := config.App{Web: config.Web{Listen: ":6410", AppURL: marker + "2"}}
	if err := ApplyOverrides(context.Background(), &cfg, &fakeSettingsDB{}); err != nil {
		t.Fatal(err)
	}
	if got := countWarns(marker+"2") - base2; got != 1 {
		t.Errorf("warnings for new value = %d, want 1", got)
	}
}

func TestLockedStoredPortWarnsOnce(t *testing.T) {
	resetWarned()
	base := countWarns("stored web port 7123 ignored")
	data := map[string]json.RawMessage{"web": json.RawMessage(`{"port":7123}`)}
	for range 3 {
		cfg := config.App{Web: config.Web{Listen: ":6410", PortLocked: true}}
		if err := ApplyOverrides(context.Background(), &cfg, &fakeSettingsDB{data: data}); err != nil {
			t.Fatal(err)
		}
	}
	if got := countWarns("stored web port 7123 ignored") - base; got != 1 {
		t.Errorf("warnings = %d, want 1", got)
	}
	same := map[string]json.RawMessage{"web": json.RawMessage(`{"port":6410}`)}
	baseSame := countWarns("stored web port 6410 ignored")
	cfg := config.App{Web: config.Web{Listen: ":6410", PortLocked: true}}
	if err := ApplyOverrides(context.Background(), &cfg, &fakeSettingsDB{data: same}); err != nil {
		t.Fatal(err)
	}
	if got := countWarns("stored web port 6410 ignored") - baseSame; got != 0 {
		t.Errorf("warned for matching port: %d", got)
	}
}

func resetWarned() {
	warnedMu.Lock()
	warned = nil
	warnedMu.Unlock()
}

func notifyLanguageJSON(t *testing.T, cfg *config.App) string {
	t.Helper()
	var s struct {
		Notifications struct {
			Language string `json:"language"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(IntegrationsJSON(cfg), &s); err != nil {
		t.Fatal(err)
	}
	return s.Notifications.Language
}

func TestIntegrationsNotifyLanguageRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := &fakeSettingsDB{}
	cfg := config.App{}
	if err := ApplyOverrides(ctx, &cfg, db); err != nil {
		t.Fatal(err)
	}
	if got := notifyLanguageJSON(t, &cfg); got != "en" {
		t.Fatalf("empty db language = %q, want en", got)
	}
	for _, step := range []struct{ patch, want string }{
		{`{"notifications":{"language":"ru"}}`, "ru"},
		{`{"notifications":{"enabled":true}}`, "ru"},
		{`{"notifications":{"language":"en"}}`, "en"},
	} {
		if err := MergeIntegrations(ctx, db, json.RawMessage(step.patch)); err != nil {
			t.Fatal(err)
		}
		cfg = config.App{}
		if err := ApplyOverrides(ctx, &cfg, db); err != nil {
			t.Fatal(err)
		}
		if cfg.Notify.Language != step.want {
			t.Fatalf("%s: cfg.Notify.Language = %q, want %q", step.patch, cfg.Notify.Language, step.want)
		}
		if got := notifyLanguageJSON(t, &cfg); got != step.want {
			t.Fatalf("%s: JSON language = %q, want %q", step.patch, got, step.want)
		}
	}
	cfg = config.App{}
	cfg.Notify.Language = "xx"
	if got := notifyLanguageJSON(t, &cfg); got != "en" {
		t.Fatalf("garbage language in JSON = %q, want en", got)
	}
}

func TestIntegrationsBrowserChannelRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := &fakeSettingsDB{}
	cfg := config.App{}
	if err := ApplyOverrides(ctx, &cfg, db); err != nil {
		t.Fatal(err)
	}
	browser := func(cfg *config.App) (bool, []string) {
		t.Helper()
		var s struct {
			Notifications struct {
				Browser *struct {
					Enabled bool `json:"enabled"`
				} `json:"browser"`
				Events []string `json:"events"`
			} `json:"notifications"`
		}
		if err := json.Unmarshal(IntegrationsJSON(cfg), &s); err != nil {
			t.Fatal(err)
		}
		if s.Notifications.Browser == nil {
			t.Fatal("notifications.browser is absent in IntegrationsJSON")
		}
		return s.Notifications.Browser.Enabled, s.Notifications.Events
	}
	enabled, events := browser(&cfg)
	if !enabled || !slices.Equal(events, notify.DefaultEvents()) {
		t.Fatalf("empty db browser = %v %v, want true %v", enabled, events, notify.DefaultEvents())
	}
	for _, step := range []struct {
		patch   string
		enabled bool
	}{
		{`{"notifications":{"events":["broken","updated","broken"],"browser":{"enabled":false}}}`, false},
		{`{"notifications":{"browser":{"enabled":true}}}`, true},
	} {
		if err := MergeIntegrations(ctx, db, json.RawMessage(step.patch)); err != nil {
			t.Fatal(err)
		}
		cfg = config.App{}
		if err := ApplyOverrides(ctx, &cfg, db); err != nil {
			t.Fatal(err)
		}
		enabled, events := browser(&cfg)
		if enabled != step.enabled || !slices.Equal(events, []string{"broken", "updated"}) {
			t.Fatalf("%s: JSON browser = %v %v", step.patch, enabled, events)
		}
		if cfg.Notify.BrowserDisabled == step.enabled || !slices.Equal(cfg.Notify.Events, []string{"broken", "updated"}) {
			t.Fatalf("%s: cfg.Notify = %v %v", step.patch, cfg.Notify.BrowserDisabled, cfg.Notify.Events)
		}
	}
}

type racingSettingsDB struct {
	mu      sync.Mutex
	data    map[string]json.RawMessage
	readers int
	second  chan struct{}
}

func (r *racingSettingsDB) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	r.mu.Lock()
	v, ok := r.data[key]
	r.readers++
	if r.readers == 2 {
		close(r.second)
	}
	r.mu.Unlock()
	select {
	case <-r.second:
	case <-time.After(200 * time.Millisecond):
	}
	return v, ok, nil
}

func (r *racingSettingsDB) SaveSetting(_ context.Context, key string, value json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[key] = value
	return nil
}

func TestMergeConcurrentKeepsBoth(t *testing.T) {
	type mergeFn func(context.Context, *racingSettingsDB, json.RawMessage) error
	cases := []struct {
		name  string
		key   string
		merge mergeFn
		a, b  string
		want  []string
	}{
		{"schedule", "schedule", func(ctx context.Context, db *racingSettingsDB, p json.RawMessage) error {
			return MergeSchedule(ctx, db, p)
		},
			`{"start_hour":5,"list_refresh_per_day":2,"threshold_month":1,"threshold_half_year":7,"threshold_year":30,"timezone":"UTC"}`,
			`{"start_hour":5,"list_refresh_per_day":2,"threshold_month":1,"threshold_half_year":7,"threshold_year":30,"monitored_categories":["tv"]}`,
			[]string{`"timezone":"UTC"`, `"monitored_categories":["tv"]`}},
		{"integrations", "integrations", func(ctx context.Context, db *racingSettingsDB, p json.RawMessage) error {
			return MergeIntegrations(ctx, db, p)
		},
			`{"qbit":{"host":"q.lan"}}`, `{"metrics":{"token":"tok"}}`, []string{`"host":"http://q.lan"`, `"token":"tok"`}},
		{"web", "web", func(ctx context.Context, db *racingSettingsDB, p json.RawMessage) error { return MergeWeb(ctx, db, p) },
			`{"app_url":"https://a.example"}`, `{"allowed_hosts":["x.lan"]}`, []string{`"app_url":"https://a.example"`, `"allowed_hosts":["x.lan"]`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := &racingSettingsDB{data: map[string]json.RawMessage{}, second: make(chan struct{})}
			var wg sync.WaitGroup
			errs := make([]error, 2)
			for i, p := range []string{tc.a, tc.b} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs[i] = tc.merge(context.Background(), db, json.RawMessage(p))
				}()
			}
			wg.Wait()
			for _, err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			got := string(db.data[tc.key])
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("lost %s in %s", w, got)
				}
			}
		})
	}
}

func TestScheduleWatchMode(t *testing.T) {
	cfg := config.App{
		Log:          config.Log{Level: "info"},
		Runner:       config.Runner{StartHour: 7},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		"schedule": json.RawMessage(`{"watch_mode":"tags"}`),
	}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.Runner.WatchMode != "tags" {
		t.Errorf("WatchMode = %q, want tags", cfg.Runner.WatchMode)
	}

	var doc struct {
		WatchMode     string `json:"watch_mode"`
		TagsSupported bool   `json:"tags_supported"`
	}
	view := config.App{Runner: config.Runner{AllowedCategories: []string{"tv"}, WatchTags: []string{"watch"}}}
	if err := json.Unmarshal(ScheduleJSON(&view), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.WatchMode != "categories" || !doc.TagsSupported {
		t.Errorf("doc = %+v, want categories and tags supported", doc)
	}
	view.Manager.Client = "deluge"
	doc.TagsSupported = true
	if err := json.Unmarshal(ScheduleJSON(&view), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.TagsSupported {
		t.Error("tags_supported = true for deluge")
	}

	_, err := SchedulePatchFromJSON(json.RawMessage(`{
		"start_hour":7,"list_refresh_per_day":2,"threshold_month":1,
		"threshold_half_year":7,"threshold_year":30,"watch_mode":"x"}`))
	if err == nil {
		t.Fatal("expected error for bad watch_mode")
	}
}
