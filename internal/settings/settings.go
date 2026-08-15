package settings

import (
	"context"
	"encoding/json"
	"fmt"

	"trackerr/internal/config"
	"trackerr/internal/storage/client/pg"
	"trackerr/internal/system/logger"
)

type settingsDB interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
}

type scheduleOverlay struct {
	StartHour             *int      `json:"start_hour,omitempty"`
	ListRefreshPerDay     *int      `json:"list_refresh_per_day,omitempty"`
	ThresholdMonth        *int      `json:"threshold_month,omitempty"`
	ThresholdHalfYear     *int      `json:"threshold_half_year,omitempty"`
	ThresholdYear         *int      `json:"threshold_year,omitempty"`
	ErrorStreakThreshold  *int      `json:"error_streak_threshold,omitempty"`
	DeleteEnabled         *bool     `json:"delete_enabled,omitempty"`
	DeleteFilesEnabled    *bool     `json:"delete_files_enabled,omitempty"`
	MonitoredCategories   *[]string `json:"monitored_categories,omitempty"`
	DeleteFilesCategories *[]string `json:"delete_files_categories,omitempty"`
	WatchTags             *[]string `json:"watch_tags,omitempty"`
	WatchTag              *string   `json:"watch_tag,omitempty"` // legacy read-only
	Timezone              *string   `json:"timezone,omitempty"`
}

type trackerItemOverlay struct {
	Enabled      *bool     `json:"enabled,omitempty"`
	Domain       *string   `json:"domain,omitempty"`
	ActiveDomain *string   `json:"active_domain,omitempty"`
	Mirrors      *[]string `json:"mirrors,omitempty"`
	HourLimit    *int      `json:"hour_limit,omitempty"`
	ProxyType    *int      `json:"proxy_type,omitempty"`
	LoginURL     *string   `json:"login_url,omitempty"`
	DownloadURL  *string   `json:"download_url,omitempty"`
	Credentials  *string   `json:"credentials,omitempty"`
	Cookies      *string   `json:"cookies,omitempty"`
	Publicity    *string   `json:"publicity,omitempty"`
}

type integrationsOverlay struct {
	Qbit struct {
		Host     *string `json:"host,omitempty"`
		Username *string `json:"username,omitempty"`
		Password *string `json:"password,omitempty"`
	} `json:"qbit"`
	Flaresolverr struct {
		URL       *string `json:"url,omitempty"`
		TimeoutMS *int    `json:"timeout_ms,omitempty"`
	} `json:"flaresolverr"`
	Proxy struct {
		DownloadProxy *string `json:"download_proxy,omitempty"`
	} `json:"proxy"`
	Notifications struct {
		Telegram struct {
			BotToken *string `json:"bot_token,omitempty"`
			ChatID   *string `json:"chat_id,omitempty"`
		} `json:"telegram"`
		Webhook struct {
			URL *string `json:"url,omitempty"`
		} `json:"webhook"`
		Enabled *bool `json:"enabled,omitempty"`
	} `json:"notifications"`
}

func ApplyOverrides(ctx context.Context, cfg *config.App, db settingsDB) error {
	if raw, ok, err := db.GetSetting(ctx, pg.SettingSchedule); err != nil {
		return err
	} else if ok {
		var o scheduleOverlay
		if err = json.Unmarshal(raw, &o); err != nil {
			logger.Warn(fmt.Sprintf("settings %s: invalid JSON overlay: %v", pg.SettingSchedule, err))
		} else {
			applySchedule(cfg, o)
		}
	}
	if raw, ok, err := db.GetSetting(ctx, pg.SettingTrackers); err != nil {
		return err
	} else if ok {
		var m map[string]trackerItemOverlay
		if err = json.Unmarshal(raw, &m); err != nil {
			logger.Warn(fmt.Sprintf("settings %s: invalid JSON overlay: %v", pg.SettingTrackers, err))
		} else {
			applyTrackers(cfg, m)
		}
	}
	if raw, ok, err := db.GetSetting(ctx, pg.SettingIntegrations); err != nil {
		return err
	} else if ok {
		var o integrationsOverlay
		if err = json.Unmarshal(raw, &o); err != nil {
			logger.Warn(fmt.Sprintf("settings %s: invalid JSON overlay: %v", pg.SettingIntegrations, err))
		} else {
			applyIntegrations(cfg, o)
		}
	}
	if err := NormalizeAppIntegrations(cfg); err != nil {
		return err
	}
	return nil
}

func applySchedule(cfg *config.App, o scheduleOverlay) {
	if o.StartHour != nil {
		cfg.Runner.StartHour = *o.StartHour
	}
	if o.ListRefreshPerDay != nil {
		cfg.Runner.ListRefreshPerDay = *o.ListRefreshPerDay
	}
	if o.ThresholdMonth != nil {
		cfg.Runner.MonthThresholdDays = *o.ThresholdMonth
	}
	if o.ThresholdHalfYear != nil {
		cfg.Runner.HalfYearThresholdDays = *o.ThresholdHalfYear
	}
	if o.ThresholdYear != nil {
		cfg.Runner.YearThresholdDays = *o.ThresholdYear
	}
	if o.ErrorStreakThreshold != nil {
		cfg.Runner.ErrorStreakThreshold = *o.ErrorStreakThreshold
	}
	if o.MonitoredCategories != nil {
		cfg.Runner.AllowedCategories = *o.MonitoredCategories
	}
	if o.WatchTags != nil {
		cfg.Runner.WatchTags = *o.WatchTags
	} else if o.WatchTag != nil {
		cfg.Runner.WatchTags = watchTagsFromLegacy(*o.WatchTag)
	}
	if o.Timezone != nil {
		cfg.Runner.Timezone = *o.Timezone
	}
	if o.DeleteEnabled != nil {
		cfg.Manager.DeleteEnabled = *o.DeleteEnabled
	}
	if o.DeleteFilesEnabled != nil {
		cfg.Manager.DeleteFilesEnabled = *o.DeleteFilesEnabled
	}
	if o.DeleteFilesCategories != nil {
		cfg.Manager.DeleteFilesCategories = *o.DeleteFilesCategories
	}
}

// applyTrackerOverlay overlays a patch onto one tracker: marshal drops the nil
// pointers (omitempty), so Unmarshal only touches the keys the patch defines.
func applyTrackerOverlay(item *config.TrackerItem, o trackerItemOverlay) error {
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, item)
}

func applyTrackers(cfg *config.App, m map[string]trackerItemOverlay) {
	for key, o := range m {
		if it := cfg.Tracker.ByKey(key); it != nil {
			_ = applyTrackerOverlay(it, o)
		}
	}
}

func ApplyTrackerKey(cfg *config.App, key string, o trackerItemOverlay) {
	if it := cfg.Tracker.ByKey(key); it != nil {
		_ = applyTrackerOverlay(it, o)
	}
}

func applyIntegrations(cfg *config.App, o integrationsOverlay) {
	if o.Qbit.Host != nil {
		cfg.Manager.QbitTorrent.Host = *o.Qbit.Host
	}
	if o.Qbit.Username != nil {
		cfg.Manager.QbitTorrent.Username = *o.Qbit.Username
	}
	if o.Qbit.Password != nil {
		cfg.Manager.QbitTorrent.Password = *o.Qbit.Password
	}
	if o.Flaresolverr.URL != nil {
		cfg.Flaresolverr.Host = *o.Flaresolverr.URL
	}
	if o.Flaresolverr.TimeoutMS != nil {
		cfg.Flaresolverr.MaxTimeout = *o.Flaresolverr.TimeoutMS
	}
	if o.Proxy.DownloadProxy != nil {
		cfg.Runner.DownloadProxy = *o.Proxy.DownloadProxy
	}
	if o.Notifications.Telegram.BotToken != nil {
		cfg.Notify.TelegramToken = *o.Notifications.Telegram.BotToken
	}
	if o.Notifications.Telegram.ChatID != nil {
		cfg.Notify.TelegramChatID = *o.Notifications.Telegram.ChatID
	}
	if o.Notifications.Webhook.URL != nil {
		cfg.Notify.WebhookURL = *o.Notifications.Webhook.URL
	}
	if o.Notifications.Enabled != nil {
		cfg.Notify.Enabled = *o.Notifications.Enabled
	}
}

func ScheduleJSON(cfg *config.App) json.RawMessage {
	s := struct {
		StartHour             int      `json:"start_hour"`
		ListRefreshPerDay     int      `json:"list_refresh_per_day"`
		ThresholdMonth        int      `json:"threshold_month"`
		ThresholdHalfYear     int      `json:"threshold_half_year"`
		ThresholdYear         int      `json:"threshold_year"`
		ErrorStreakThreshold  int      `json:"error_streak_threshold"`
		DeleteEnabled         bool     `json:"delete_enabled"`
		DeleteFilesEnabled    bool     `json:"delete_files_enabled"`
		MonitoredCategories   []string `json:"monitored_categories"`
		DeleteFilesCategories []string `json:"delete_files_categories"`
		WatchTags             []string `json:"watch_tags"`
		Timezone              string   `json:"timezone"`
	}{
		StartHour:            cfg.Runner.StartHour,
		ListRefreshPerDay:    cfg.Runner.ListRefreshPerDay,
		ThresholdMonth:       cfg.Runner.MonthThresholdDays,
		ThresholdHalfYear:    cfg.Runner.HalfYearThresholdDays,
		ThresholdYear:        cfg.Runner.YearThresholdDays,
		ErrorStreakThreshold: cfg.Runner.ErrorStreakThreshold,
		MonitoredCategories:  cfg.Runner.AllowedCategories,
		WatchTags:            cfg.Runner.WatchTags,
		Timezone:             cfg.Runner.Timezone,
	}
	s.DeleteEnabled = cfg.Manager.DeleteEnabled
	s.DeleteFilesEnabled = cfg.Manager.DeleteFilesEnabled
	s.DeleteFilesCategories = cfg.Manager.DeleteFilesCategories
	if s.MonitoredCategories == nil {
		s.MonitoredCategories = []string{}
	}
	if s.DeleteFilesCategories == nil {
		s.DeleteFilesCategories = []string{}
	}
	if s.WatchTags == nil {
		s.WatchTags = []string{}
	}
	b, _ := json.Marshal(s)
	return b
}

func IntegrationsJSON(cfg *config.App) json.RawMessage {
	s := struct {
		Qbit struct {
			Host     string `json:"host"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"qbit"`
		Flaresolverr struct {
			URL       string `json:"url"`
			TimeoutMS int    `json:"timeout_ms"`
		} `json:"flaresolverr"`
		Proxy struct {
			DownloadProxy string `json:"download_proxy"`
		} `json:"proxy"`
		Notifications struct {
			Telegram struct {
				BotToken string `json:"bot_token"`
				ChatID   string `json:"chat_id"`
			} `json:"telegram"`
			Webhook struct {
				URL string `json:"url"`
			} `json:"webhook"`
			Enabled bool `json:"enabled"`
		} `json:"notifications"`
	}{}
	s.Qbit.Host = cfg.Manager.QbitTorrent.Host
	s.Qbit.Username = cfg.Manager.QbitTorrent.Username
	s.Qbit.Password = cfg.Manager.QbitTorrent.Password
	s.Flaresolverr.URL = cfg.Flaresolverr.Host
	s.Flaresolverr.TimeoutMS = cfg.Flaresolverr.MaxTimeout
	s.Proxy.DownloadProxy = cfg.Runner.DownloadProxy
	s.Notifications.Telegram.BotToken = cfg.Notify.TelegramToken
	s.Notifications.Telegram.ChatID = cfg.Notify.TelegramChatID
	s.Notifications.Webhook.URL = cfg.Notify.WebhookURL
	s.Notifications.Enabled = cfg.Notify.Enabled
	b, _ := json.Marshal(s)
	return b
}

func TrackersEffective(ctx context.Context, cfg *config.App, db settingsDB) (map[string]config.TrackerItem, error) {
	m := make(map[string]config.TrackerItem, len(config.TrackerKeys))
	for _, key := range config.TrackerKeys {
		if it := cfg.Tracker.ByKey(key); it != nil {
			m[key] = *it
		}
	}
	if raw, ok, err := db.GetSetting(ctx, pg.SettingTrackers); err != nil {
		return nil, err
	} else if ok {
		var overlay map[string]trackerItemOverlay
		if err = json.Unmarshal(raw, &overlay); err != nil {
			return nil, err
		}
		for k, o := range overlay {
			it, exists := m[k]
			if !exists {
				continue
			}
			if err = applyTrackerOverlay(&it, o); err != nil {
				return nil, err
			}
			m[k] = it
		}
	}
	for k, it := range m {
		if it.Mirrors == nil {
			it.Mirrors = []string{}
			m[k] = it
		}
	}
	return m, nil
}

// mergePatch overlays filled fields from patch onto dst: during marshal nil-pointers
// are dropped (omitempty), so Unmarshal only touches keys that the patch defines.
func mergePatch[T any](dst *T, patch T) error {
	b, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func MergeSchedule(ctx context.Context, db interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}, patch json.RawMessage) error {
	overlay, err := SchedulePatchFromJSON(patch)
	if err != nil {
		return err
	}
	var existing scheduleOverlay
	if raw, ok, err := db.GetSetting(ctx, pg.SettingSchedule); err != nil {
		return err
	} else if ok {
		if err = json.Unmarshal(raw, &existing); err != nil {
			return err
		}
	}
	if err = mergePatch(&existing, overlay); err != nil {
		return err
	}
	if overlay.WatchTags != nil {
		existing.WatchTag = nil
	}
	out, err := json.Marshal(existing)
	if err != nil {
		return err
	}
	return db.SaveSetting(ctx, pg.SettingSchedule, out)
}

func MergeTrackerKey(ctx context.Context, db interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}, key string, patch json.RawMessage) error {
	overlay, err := TrackerPatchFromJSON(patch)
	if err != nil {
		return err
	}
	var m map[string]trackerItemOverlay
	if raw, ok, err := db.GetSetting(ctx, pg.SettingTrackers); err != nil {
		return err
	} else if ok {
		if err = json.Unmarshal(raw, &m); err != nil {
			return err
		}
	}
	if m == nil {
		m = make(map[string]trackerItemOverlay)
	}
	item := m[key]
	if err = mergePatch(&item, overlay); err != nil {
		return err
	}
	m[key] = item
	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return db.SaveSetting(ctx, pg.SettingTrackers, out)
}

func MergeIntegrations(ctx context.Context, db interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}, patch json.RawMessage) error {
	overlay, err := IntegrationsPatchFromJSON(patch)
	if err != nil {
		return err
	}
	var existing integrationsOverlay
	if raw, ok, err := db.GetSetting(ctx, pg.SettingIntegrations); err != nil {
		return err
	} else if ok {
		if err = json.Unmarshal(raw, &existing); err != nil {
			return err
		}
	}
	if err = mergePatch(&existing, overlay); err != nil {
		return err
	}
	out, err := json.Marshal(existing)
	if err != nil {
		return err
	}
	return db.SaveSetting(ctx, pg.SettingIntegrations, out)
}
