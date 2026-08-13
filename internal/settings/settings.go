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
	StartHour             *int      `json:"start_hour"`
	ListRefreshPerDay     *int      `json:"list_refresh_per_day"`
	ThresholdMonth        *int      `json:"threshold_month"`
	ThresholdHalfYear     *int      `json:"threshold_half_year"`
	ThresholdYear         *int      `json:"threshold_year"`
	ErrorStreakThreshold  *int      `json:"error_streak_threshold"`
	DeleteEnabled         *bool     `json:"delete_enabled"`
	DeleteFilesEnabled    *bool     `json:"delete_files_enabled"`
	MonitoredCategories   *[]string `json:"monitored_categories"`
	DeleteFilesCategories *[]string `json:"delete_files_categories"`
	WatchTags             *[]string `json:"watch_tags"`
	WatchTag              *string   `json:"watch_tag"` // legacy read-only
	Timezone              *string   `json:"timezone"`
}

type trackerItemOverlay struct {
	Enabled      *bool     `json:"enabled"`
	Domain       *string   `json:"domain"`
	ActiveDomain *string   `json:"active_domain"`
	Mirrors      *[]string `json:"mirrors"`
	HourLimit    *int      `json:"hour_limit"`
	ProxyType    *int      `json:"proxy_type"`
	LoginURL     *string   `json:"login_url"`
	DownloadURL  *string   `json:"download_url"`
	Credentials  *string   `json:"credentials"`
	Cookies      *string   `json:"cookies"`
	Publicity    *string   `json:"publicity"`
}

type integrationsOverlay struct {
	Qbit struct {
		Host     *string `json:"host"`
		Username *string `json:"username"`
		Password *string `json:"password"`
	} `json:"qbit"`
	Flaresolverr struct {
		URL       *string `json:"url"`
		TimeoutMS *int    `json:"timeout_ms"`
	} `json:"flaresolverr"`
	Proxy struct {
		DownloadProxy *string `json:"download_proxy"`
	} `json:"proxy"`
}

// ApplyOverrides loads settings from DB and overlays them on cfg (missing keys keep env values).
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
	if cfg.Runner == nil {
		return
	}
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
	if cfg.Manager != nil {
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
}

func applyTrackers(cfg *config.App, m map[string]trackerItemOverlay) {
	if cfg.Tracker == nil {
		return
	}
	applyOne := func(key string, f *trackerFields) {
		if o, ok := m[key]; ok {
			overlayTracker(o, f)
		}
	}
	applyOne("rutracker", &trackerFields{
		Enabled: &cfg.Tracker.Rutracker.Enabled, Domain: &cfg.Tracker.Rutracker.Domain,
		ActiveDomain: &cfg.Tracker.Rutracker.ActiveDomain,
		Mirrors:      &cfg.Tracker.Rutracker.Mirrors,
		HourLimit:    &cfg.Tracker.Rutracker.HourLimit, ProxyType: &cfg.Tracker.Rutracker.ProxyType,
		LoginURL: &cfg.Tracker.Rutracker.LoginURL, DownloadURL: &cfg.Tracker.Rutracker.DownloadURL,
		Credentials: &cfg.Tracker.Rutracker.Credentials, Publicity: &cfg.Tracker.Rutracker.Publicity,
	})
	applyOne("kinozal", &trackerFields{
		Enabled: &cfg.Tracker.Kinozal.Enabled, Domain: &cfg.Tracker.Kinozal.Domain,
		ActiveDomain: &cfg.Tracker.Kinozal.ActiveDomain,
		Mirrors:      &cfg.Tracker.Kinozal.Mirrors,
		HourLimit:    &cfg.Tracker.Kinozal.HourLimit, ProxyType: &cfg.Tracker.Kinozal.ProxyType,
		LoginURL: &cfg.Tracker.Kinozal.LoginURL, DownloadURL: &cfg.Tracker.Kinozal.DownloadURL,
		Credentials: &cfg.Tracker.Kinozal.Credentials, Cookies: &cfg.Tracker.Kinozal.Cookies,
		Publicity: &cfg.Tracker.Kinozal.Publicity,
	})
	applyOne("rutor", &trackerFields{
		Enabled: &cfg.Tracker.Rutor.Enabled, Domain: &cfg.Tracker.Rutor.Domain,
		ActiveDomain: &cfg.Tracker.Rutor.ActiveDomain,
		Mirrors:      &cfg.Tracker.Rutor.Mirrors,
		HourLimit:    &cfg.Tracker.Rutor.HourLimit, DownloadURL: &cfg.Tracker.Rutor.DownloadURL,
		Publicity: &cfg.Tracker.Rutor.Publicity,
	})
	applyOne(&trackerFields{
	})
	applyOne("nnm", &trackerFields{
		Enabled: &cfg.Tracker.NNM.Enabled, Domain: &cfg.Tracker.NNM.Domain,
		ActiveDomain: &cfg.Tracker.NNM.ActiveDomain,
		Mirrors:      &cfg.Tracker.NNM.Mirrors,
		HourLimit:    &cfg.Tracker.NNM.HourLimit, ProxyType: &cfg.Tracker.NNM.ProxyType,
		LoginURL: &cfg.Tracker.NNM.LoginURL, DownloadURL: &cfg.Tracker.NNM.DownloadURL,
		Credentials: &cfg.Tracker.NNM.Credentials, Cookies: &cfg.Tracker.NNM.Cookies,
		Publicity: &cfg.Tracker.NNM.Publicity,
	})
	applyOne("teamhd", &trackerFields{
		Enabled: &cfg.Tracker.TeamHD.Enabled, Domain: &cfg.Tracker.TeamHD.Domain,
		ActiveDomain: &cfg.Tracker.TeamHD.ActiveDomain,
		Mirrors:      &cfg.Tracker.TeamHD.Mirrors,
		HourLimit:    &cfg.Tracker.TeamHD.HourLimit, DownloadURL: &cfg.Tracker.TeamHD.DownloadURL,
		Cookies: &cfg.Tracker.TeamHD.Cookies, Publicity: &cfg.Tracker.TeamHD.Publicity,
	})
}

// ApplyTrackerKey overlays one tracker key onto cfg (partial patch; unset fields keep current values).
func ApplyTrackerKey(cfg *config.App, key string, o trackerItemOverlay) {
	if cfg.Tracker == nil {
		return
	}
	applyOne := func(k string, f *trackerFields) {
		if k == key {
			overlayTracker(o, f)
		}
	}
	applyOne("rutracker", &trackerFields{
		Enabled: &cfg.Tracker.Rutracker.Enabled, Domain: &cfg.Tracker.Rutracker.Domain,
		ActiveDomain: &cfg.Tracker.Rutracker.ActiveDomain,
		Mirrors:      &cfg.Tracker.Rutracker.Mirrors,
		HourLimit:    &cfg.Tracker.Rutracker.HourLimit, ProxyType: &cfg.Tracker.Rutracker.ProxyType,
		LoginURL: &cfg.Tracker.Rutracker.LoginURL, DownloadURL: &cfg.Tracker.Rutracker.DownloadURL,
		Credentials: &cfg.Tracker.Rutracker.Credentials, Publicity: &cfg.Tracker.Rutracker.Publicity,
	})
	applyOne("kinozal", &trackerFields{
		Enabled: &cfg.Tracker.Kinozal.Enabled, Domain: &cfg.Tracker.Kinozal.Domain,
		ActiveDomain: &cfg.Tracker.Kinozal.ActiveDomain,
		Mirrors:      &cfg.Tracker.Kinozal.Mirrors,
		HourLimit:    &cfg.Tracker.Kinozal.HourLimit, ProxyType: &cfg.Tracker.Kinozal.ProxyType,
		LoginURL: &cfg.Tracker.Kinozal.LoginURL, DownloadURL: &cfg.Tracker.Kinozal.DownloadURL,
		Credentials: &cfg.Tracker.Kinozal.Credentials, Cookies: &cfg.Tracker.Kinozal.Cookies,
		Publicity: &cfg.Tracker.Kinozal.Publicity,
	})
	applyOne("rutor", &trackerFields{
		Enabled: &cfg.Tracker.Rutor.Enabled, Domain: &cfg.Tracker.Rutor.Domain,
		ActiveDomain: &cfg.Tracker.Rutor.ActiveDomain,
		Mirrors:      &cfg.Tracker.Rutor.Mirrors,
		HourLimit:    &cfg.Tracker.Rutor.HourLimit, DownloadURL: &cfg.Tracker.Rutor.DownloadURL,
		Publicity: &cfg.Tracker.Rutor.Publicity,
	})
	applyOne(&trackerFields{
	})
	applyOne("nnm", &trackerFields{
		Enabled: &cfg.Tracker.NNM.Enabled, Domain: &cfg.Tracker.NNM.Domain,
		ActiveDomain: &cfg.Tracker.NNM.ActiveDomain,
		Mirrors:      &cfg.Tracker.NNM.Mirrors,
		HourLimit:    &cfg.Tracker.NNM.HourLimit, ProxyType: &cfg.Tracker.NNM.ProxyType,
		LoginURL: &cfg.Tracker.NNM.LoginURL, DownloadURL: &cfg.Tracker.NNM.DownloadURL,
		Credentials: &cfg.Tracker.NNM.Credentials, Cookies: &cfg.Tracker.NNM.Cookies,
		Publicity: &cfg.Tracker.NNM.Publicity,
	})
	applyOne("teamhd", &trackerFields{
		Enabled: &cfg.Tracker.TeamHD.Enabled, Domain: &cfg.Tracker.TeamHD.Domain,
		ActiveDomain: &cfg.Tracker.TeamHD.ActiveDomain,
		Mirrors:      &cfg.Tracker.TeamHD.Mirrors,
		HourLimit:    &cfg.Tracker.TeamHD.HourLimit, DownloadURL: &cfg.Tracker.TeamHD.DownloadURL,
		Cookies: &cfg.Tracker.TeamHD.Cookies, Publicity: &cfg.Tracker.TeamHD.Publicity,
	})
}

type trackerFields struct {
	Enabled      *bool
	Domain       *string
	ActiveDomain *string
	Mirrors      *[]string
	HourLimit    *int
	ProxyType    *int
	LoginURL     *string
	DownloadURL  *string
	Credentials  *string
	Cookies      *string
	Publicity    *string
}

func overlayTracker(o trackerItemOverlay, t *trackerFields) {
	if o.Enabled != nil {
		*t.Enabled = *o.Enabled
	}
	if o.Domain != nil {
		*t.Domain = *o.Domain
	}
	if o.ActiveDomain != nil {
		*t.ActiveDomain = *o.ActiveDomain
	}
	if o.Mirrors != nil {
		*t.Mirrors = *o.Mirrors
	}
	if o.HourLimit != nil {
		*t.HourLimit = *o.HourLimit
	}
	if o.ProxyType != nil && t.ProxyType != nil {
		*t.ProxyType = *o.ProxyType
	}
	if o.LoginURL != nil && t.LoginURL != nil {
		*t.LoginURL = *o.LoginURL
	}
	if o.DownloadURL != nil && t.DownloadURL != nil {
		*t.DownloadURL = *o.DownloadURL
	}
	if o.Credentials != nil && t.Credentials != nil {
		*t.Credentials = *o.Credentials
	}
	if o.Cookies != nil && t.Cookies != nil {
		*t.Cookies = *o.Cookies
	}
	if o.Publicity != nil {
		*t.Publicity = *o.Publicity
	}
}

func applyIntegrations(cfg *config.App, o integrationsOverlay) {
	if cfg.Manager != nil {
		if o.Qbit.Host != nil {
			cfg.Manager.QbitTorrent.Host = *o.Qbit.Host
		}
		if o.Qbit.Username != nil {
			cfg.Manager.QbitTorrent.Username = *o.Qbit.Username
		}
		if o.Qbit.Password != nil {
			cfg.Manager.QbitTorrent.Password = *o.Qbit.Password
		}
	}
	if cfg.Flaresolverr != nil {
		if o.Flaresolverr.URL != nil {
			cfg.Flaresolverr.Host = *o.Flaresolverr.URL
		}
		if o.Flaresolverr.TimeoutMS != nil {
			cfg.Flaresolverr.MaxTimeout = *o.Flaresolverr.TimeoutMS
		}
	}
	if cfg.Runner != nil && o.Proxy.DownloadProxy != nil {
		cfg.Runner.DownloadProxy = *o.Proxy.DownloadProxy
	}
}

// ScheduleJSON returns the effective schedule as JSON for API responses.
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
	if cfg.Manager != nil {
		s.DeleteEnabled = cfg.Manager.DeleteEnabled
		s.DeleteFilesEnabled = cfg.Manager.DeleteFilesEnabled
		s.DeleteFilesCategories = cfg.Manager.DeleteFilesCategories
	}
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

// IntegrationsJSON returns effective integrations as JSON.
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
	}{}
	s.Qbit.Host = cfg.Manager.QbitTorrent.Host
	s.Qbit.Username = cfg.Manager.QbitTorrent.Username
	s.Qbit.Password = cfg.Manager.QbitTorrent.Password
	s.Flaresolverr.URL = cfg.Flaresolverr.Host
	s.Flaresolverr.TimeoutMS = cfg.Flaresolverr.MaxTimeout
	s.Proxy.DownloadProxy = cfg.Runner.DownloadProxy
	b, _ := json.Marshal(s)
	return b
}

// TrackersJSON returns effective tracker settings merged from env + DB overlay document.
func TrackersJSON(ctx context.Context, cfg *config.App, db settingsDB) (json.RawMessage, error) {
	type item struct {
		Enabled      bool     `json:"enabled"`
		Domain       string   `json:"domain"`
		ActiveDomain string   `json:"active_domain"`
		Mirrors      []string `json:"mirrors"`
		HourLimit    int      `json:"hour_limit"`
		ProxyType    int      `json:"proxy_type"`
		LoginURL     string   `json:"login_url"`
		DownloadURL  string   `json:"download_url"`
		Credentials  string   `json:"credentials"`
		Cookies      string   `json:"cookies"`
		Publicity    string   `json:"publicity"`
	}
	m := map[string]item{
		"rutracker": {cfg.Tracker.Rutracker.Enabled, cfg.Tracker.Rutracker.Domain, cfg.Tracker.Rutracker.ActiveDomain, cfg.Tracker.Rutracker.Mirrors, cfg.Tracker.Rutracker.HourLimit, cfg.Tracker.Rutracker.ProxyType, cfg.Tracker.Rutracker.LoginURL, cfg.Tracker.Rutracker.DownloadURL, cfg.Tracker.Rutracker.Credentials, "", cfg.Tracker.Rutracker.Publicity},
		"kinozal":   {cfg.Tracker.Kinozal.Enabled, cfg.Tracker.Kinozal.Domain, cfg.Tracker.Kinozal.ActiveDomain, cfg.Tracker.Kinozal.Mirrors, cfg.Tracker.Kinozal.HourLimit, cfg.Tracker.Kinozal.ProxyType, cfg.Tracker.Kinozal.LoginURL, cfg.Tracker.Kinozal.DownloadURL, cfg.Tracker.Kinozal.Credentials, cfg.Tracker.Kinozal.Cookies, cfg.Tracker.Kinozal.Publicity},
		"rutor":     {cfg.Tracker.Rutor.Enabled, cfg.Tracker.Rutor.Domain, cfg.Tracker.Rutor.ActiveDomain, cfg.Tracker.Rutor.Mirrors, cfg.Tracker.Rutor.HourLimit, 0, "", cfg.Tracker.Rutor.DownloadURL, "", "", cfg.Tracker.Rutor.Publicity},
		"nnm":       {cfg.Tracker.NNM.Enabled, cfg.Tracker.NNM.Domain, cfg.Tracker.NNM.ActiveDomain, cfg.Tracker.NNM.Mirrors, cfg.Tracker.NNM.HourLimit, cfg.Tracker.NNM.ProxyType, cfg.Tracker.NNM.LoginURL, cfg.Tracker.NNM.DownloadURL, cfg.Tracker.NNM.Credentials, cfg.Tracker.NNM.Cookies, cfg.Tracker.NNM.Publicity},
		"teamhd":    {cfg.Tracker.TeamHD.Enabled, cfg.Tracker.TeamHD.Domain, cfg.Tracker.TeamHD.ActiveDomain, cfg.Tracker.TeamHD.Mirrors, cfg.Tracker.TeamHD.HourLimit, 0, "", cfg.Tracker.TeamHD.DownloadURL, "", cfg.Tracker.TeamHD.Cookies, cfg.Tracker.TeamHD.Publicity},
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
			if o.Enabled != nil {
				it.Enabled = *o.Enabled
			}
			if o.Domain != nil {
				it.Domain = *o.Domain
			}
			if o.ActiveDomain != nil {
				it.ActiveDomain = *o.ActiveDomain
			}
			if o.Mirrors != nil {
				it.Mirrors = *o.Mirrors
			}
			if o.HourLimit != nil {
				it.HourLimit = *o.HourLimit
			}
			if o.ProxyType != nil {
				it.ProxyType = *o.ProxyType
			}
			if o.LoginURL != nil {
				it.LoginURL = *o.LoginURL
			}
			if o.DownloadURL != nil {
				it.DownloadURL = *o.DownloadURL
			}
			if o.Credentials != nil {
				it.Credentials = *o.Credentials
			}
			if o.Cookies != nil {
				it.Cookies = *o.Cookies
			}
			if o.Publicity != nil {
				it.Publicity = *o.Publicity
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
	return json.Marshal(m)
}

func mergeTrackerOverlay(dst, src trackerItemOverlay) trackerItemOverlay {
	if src.Enabled != nil {
		dst.Enabled = src.Enabled
	}
	if src.Domain != nil {
		dst.Domain = src.Domain
	}
	if src.ActiveDomain != nil {
		dst.ActiveDomain = src.ActiveDomain
	}
	if src.Mirrors != nil {
		dst.Mirrors = src.Mirrors
	}
	if src.HourLimit != nil {
		dst.HourLimit = src.HourLimit
	}
	if src.ProxyType != nil {
		dst.ProxyType = src.ProxyType
	}
	if src.LoginURL != nil {
		dst.LoginURL = src.LoginURL
	}
	if src.DownloadURL != nil {
		dst.DownloadURL = src.DownloadURL
	}
	if src.Credentials != nil {
		dst.Credentials = src.Credentials
	}
	if src.Cookies != nil {
		dst.Cookies = src.Cookies
	}
	if src.Publicity != nil {
		dst.Publicity = src.Publicity
	}
	return dst
}

func mergeScheduleOverlay(dst, src scheduleOverlay) scheduleOverlay {
	if src.StartHour != nil {
		dst.StartHour = src.StartHour
	}
	if src.ListRefreshPerDay != nil {
		dst.ListRefreshPerDay = src.ListRefreshPerDay
	}
	if src.ThresholdMonth != nil {
		dst.ThresholdMonth = src.ThresholdMonth
	}
	if src.ThresholdHalfYear != nil {
		dst.ThresholdHalfYear = src.ThresholdHalfYear
	}
	if src.ThresholdYear != nil {
		dst.ThresholdYear = src.ThresholdYear
	}
	if src.ErrorStreakThreshold != nil {
		dst.ErrorStreakThreshold = src.ErrorStreakThreshold
	}
	if src.MonitoredCategories != nil {
		dst.MonitoredCategories = src.MonitoredCategories
	}
	if src.DeleteEnabled != nil {
		dst.DeleteEnabled = src.DeleteEnabled
	}
	if src.DeleteFilesEnabled != nil {
		dst.DeleteFilesEnabled = src.DeleteFilesEnabled
	}
	if src.DeleteFilesCategories != nil {
		dst.DeleteFilesCategories = src.DeleteFilesCategories
	}
	if src.WatchTags != nil {
		dst.WatchTags = src.WatchTags
		dst.WatchTag = nil
	}
	if src.Timezone != nil {
		dst.Timezone = src.Timezone
	}
	return dst
}

// MergeSchedule merges a patch into the stored schedule document.
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
	merged := mergeScheduleOverlay(existing, overlay)
	out, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return db.SaveSetting(ctx, pg.SettingSchedule, out)
}

func mergeIntegrationsOverlay(dst, src integrationsOverlay) integrationsOverlay {
	if src.Qbit.Host != nil {
		dst.Qbit.Host = src.Qbit.Host
	}
	if src.Qbit.Username != nil {
		dst.Qbit.Username = src.Qbit.Username
	}
	if src.Qbit.Password != nil {
		dst.Qbit.Password = src.Qbit.Password
	}
	if src.Flaresolverr.URL != nil {
		dst.Flaresolverr.URL = src.Flaresolverr.URL
	}
	if src.Flaresolverr.TimeoutMS != nil {
		dst.Flaresolverr.TimeoutMS = src.Flaresolverr.TimeoutMS
	}
	if src.Proxy.DownloadProxy != nil {
		dst.Proxy.DownloadProxy = src.Proxy.DownloadProxy
	}
	return dst
}

// MergeTrackerKey merges one tracker key into the stored trackers document.
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
	m[key] = mergeTrackerOverlay(m[key], overlay)
	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return db.SaveSetting(ctx, pg.SettingTrackers, out)
}

// MergeIntegrations merges a patch into the stored integrations document.
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
	merged := mergeIntegrationsOverlay(existing, overlay)
	out, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return db.SaveSetting(ctx, pg.SettingIntegrations, out)
}
