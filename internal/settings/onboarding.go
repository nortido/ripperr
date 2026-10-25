package settings

import (
	"context"
	"encoding/json"
	"fmt"

	"ripperr/internal/config"
	"ripperr/internal/storage/client/sqlite"
)

const (
	settingOnboarding = "onboarding"
	defaultWatchTag   = "watch"
)

type Onboarding struct {
	ChecklistHidden bool `json:"checklist_hidden"`
	TourDone        bool `json:"tour_done"`
	Replay          bool `json:"replay"`
}

type OnboardingPatch struct {
	ChecklistHidden *bool `json:"checklist_hidden,omitempty"`
	TourDone        *bool `json:"tour_done,omitempty"`
	Replay          *bool `json:"replay,omitempty"`
}

func GetOnboarding(ctx context.Context, db settingsDB) (Onboarding, error) {
	var d Onboarding
	raw, ok, err := db.GetSetting(ctx, settingOnboarding)
	if err != nil || !ok {
		return d, err
	}
	if err = json.Unmarshal(raw, &d); err != nil {
		warnOnce(fmt.Sprintf("settings %s: invalid JSON: %v", settingOnboarding, err))
		return Onboarding{}, nil
	}
	return d, nil
}

func MergeOnboarding(ctx context.Context, db interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}, p OnboardingPatch) error {
	mergeMu.Lock()
	defer mergeMu.Unlock()
	var d Onboarding
	if raw, ok, err := db.GetSetting(ctx, settingOnboarding); err != nil {
		return err
	} else if ok && json.Unmarshal(raw, &d) != nil {
		// косметический документ должен чиниться из UI, поэтому битый просто перезаписываем
		d = Onboarding{}
	}
	if p.ChecklistHidden != nil {
		d.ChecklistHidden = *p.ChecklistHidden
	}
	if p.TourDone != nil {
		d.TourDone = *p.TourDone
	}
	if p.Replay != nil {
		d.Replay = *p.Replay
	}
	out, err := json.Marshal(d)
	if err != nil {
		return err
	}
	return db.SaveSetting(ctx, settingOnboarding, out)
}

func WatchChosen(ctx context.Context, db settingsDB, cfg *config.App) (bool, error) {
	if len(cfg.Runner.AllowedCategories) > 0 {
		return true, nil
	}
	// тег "watch" по умолчанию выбором не считается, а RUNNER_TAG=tv считается
	if tags := cfg.Runner.WatchTags; len(tags) > 1 || len(tags) == 1 && tags[0] != defaultWatchTag {
		return true, nil
	}
	raw, ok, err := db.GetSetting(ctx, sqlite.SettingSchedule)
	if err != nil || !ok {
		return false, err
	}
	var o scheduleOverlay
	if err = json.Unmarshal(raw, &o); err != nil {
		return false, nil
	}
	return o.WatchMode != nil || o.MonitoredCategories != nil || o.WatchTags != nil || o.WatchTag != nil, nil
}
