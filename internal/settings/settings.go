package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strconv"
	"sync"

	"ripperr/internal/config"
	"ripperr/internal/notify"
	"ripperr/internal/storage/client/sqlite"
	"ripperr/internal/system/logger"
)

const settingWeb = "web"

var (
	warnedMu sync.Mutex
	warned   map[string]struct{}

	// GetSetting..SaveSetting у Merge* не атомарны: без замка параллельные сохранения теряют соседние поля
	mergeMu sync.Mutex
)

// ApplyOverrides идет на каждый effective(), поэтому одинаковое предупреждение пишем один раз
func warnOnce(msg string) {
	warnedMu.Lock()
	_, seen := warned[msg]
	if !seen {
		if warned == nil {
			warned = make(map[string]struct{})
		}
		warned[msg] = struct{}{}
	}
	warnedMu.Unlock()
	if !seen {
		logger.Warn(msg)
	}
}

const settingTrackersAdded = "trackers_added"

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
	MagnetTimeout         *int64    `json:"magnet_timeout,omitempty"`
	DeleteEnabled         *bool     `json:"delete_enabled,omitempty"`
	DeleteFilesEnabled    *bool     `json:"delete_files_enabled,omitempty"`
	MirrorConfirm         *bool     `json:"mirror_confirm,omitempty"`
	RunOnStart            *bool     `json:"run_on_start,omitempty"`
	MonitoredCategories   *[]string `json:"monitored_categories,omitempty"`
	DeleteFilesCategories *[]string `json:"delete_files_categories,omitempty"`
	WatchTags             *[]string `json:"watch_tags,omitempty"`
	WatchTag              *string   `json:"watch_tag,omitempty"` // устаревшее, только на чтение
	WatchMode             *string   `json:"watch_mode,omitempty"`
	Timezone              *string   `json:"timezone,omitempty"`
}

type trackerItemOverlay struct {
	Enabled      *bool     `json:"enabled,omitempty"`
	Domain       *string   `json:"domain,omitempty"`
	ActiveDomain *string   `json:"active_domain,omitempty"`
	Mirrors      *[]string `json:"mirrors,omitempty"`
	Retired      *[]string `json:"retired,omitempty"`
	Unconfirmed  *[]string `json:"unconfirmed,omitempty"`
	HourLimit    *int      `json:"hour_limit,omitempty"`
	ProxyType    *int      `json:"proxy_type,omitempty"`
	LoginURL     *string   `json:"login_url,omitempty"`
	DownloadURL  *string   `json:"download_url,omitempty"`
	Credentials  *string   `json:"credentials,omitempty"`
	Login        *string   `json:"login,omitempty"`
	Password     *string   `json:"password,omitempty"`
	Cookies      *string   `json:"cookies,omitempty"`
	Publicity    *string   `json:"publicity,omitempty"`
}

type integrationsOverlay struct {
	Client *string `json:"client,omitempty"`
	Qbit   struct {
		Host     *string `json:"host,omitempty"`
		Username *string `json:"username,omitempty"`
		Password *string `json:"password,omitempty"`
	} `json:"qbit"`
	Transmission struct {
		Host     *string `json:"host,omitempty"`
		Username *string `json:"username,omitempty"`
		Password *string `json:"password,omitempty"`
	} `json:"transmission"`
	Deluge struct {
		Host     *string `json:"host,omitempty"`
		Password *string `json:"password,omitempty"`
	} `json:"deluge"`
	Flaresolverr struct {
		URL       *string `json:"url,omitempty"`
		TimeoutMS *int    `json:"timeout_ms,omitempty"`
	} `json:"flaresolverr"`
	Proxy struct {
		DownloadProxy *string `json:"download_proxy,omitempty"`
	} `json:"proxy"`
	Notifications struct {
		Telegram struct {
			Enabled  *bool     `json:"enabled,omitempty"`
			BotToken *string   `json:"bot_token,omitempty"`
			ChatID   *string   `json:"chat_id,omitempty"`
			Events   *[]string `json:"events,omitempty"`
		} `json:"telegram"`
		Webhook struct {
			Enabled *bool     `json:"enabled,omitempty"`
			URL     *string   `json:"url,omitempty"`
			Events  *[]string `json:"events,omitempty"`
		} `json:"webhook"`
		Email struct {
			Enabled  *bool     `json:"enabled,omitempty"`
			Host     *string   `json:"host,omitempty"`
			Port     *int      `json:"port,omitempty"`
			Security *string   `json:"security,omitempty"`
			Username *string   `json:"username,omitempty"`
			Password *string   `json:"password,omitempty"`
			From     *string   `json:"from,omitempty"`
			To       *string   `json:"to,omitempty"`
			Events   *[]string `json:"events,omitempty"`
		} `json:"email"`
		Pushover struct {
			Enabled  *bool     `json:"enabled,omitempty"`
			AppToken *string   `json:"app_token,omitempty"`
			UserKey  *string   `json:"user_key,omitempty"`
			Events   *[]string `json:"events,omitempty"`
		} `json:"pushover"`
		Browser struct {
			Enabled *bool     `json:"enabled,omitempty"`
			Events  *[]string `json:"events,omitempty"`
		} `json:"browser"`
		Enabled  *bool     `json:"enabled,omitempty"`
		Language *string   `json:"language,omitempty"`
		Events   *[]string `json:"events,omitempty"`
	} `json:"notifications"`
	Metrics struct {
		Token *string `json:"token,omitempty"`
	} `json:"metrics"`
	MirrorSource struct {
		Type   *string `json:"type,omitempty"`
		URL    *string `json:"url,omitempty"`
		Direct *bool   `json:"direct,omitempty"`
	} `json:"mirror_source"`
}

type InvalidConfigError struct{ Err error }

func (e *InvalidConfigError) Error() string { return e.Err.Error() }

func (e *InvalidConfigError) Unwrap() error { return e.Err }

func ApplyOverrides(ctx context.Context, cfg *config.App, db settingsDB) error {
	// вызывающий передает копию App, но трекеры лежат в map. Без своей копии патч из БД
	// ушел бы в базовый конфиг и пережил бы удаление настройки
	cfg.Tracker = cfg.Tracker.Clone()
	if raw, ok, err := db.GetSetting(ctx, sqlite.SettingSchedule); err != nil {
		return err
	} else if ok {
		var o scheduleOverlay
		if err = json.Unmarshal(raw, &o); err != nil {
			logger.Warn(fmt.Sprintf("settings %s: invalid JSON overlay: %v", sqlite.SettingSchedule, err))
		} else {
			applySchedule(cfg, o)
		}
	}
	if raw, ok, err := db.GetSetting(ctx, sqlite.SettingTrackers); err != nil {
		return err
	} else if ok {
		var m map[string]trackerItemOverlay
		if err = json.Unmarshal(raw, &m); err != nil {
			logger.Warn(fmt.Sprintf("settings %s: invalid JSON overlay: %v", sqlite.SettingTrackers, err))
		} else {
			applyTrackers(cfg, m)
		}
	}
	if added, ok, err := TrackersAdded(ctx, db); err != nil {
		return err
	} else if ok {
		for key, it := range cfg.Tracker {
			if !slices.Contains(added, key) {
				it.Enabled = false
			}
		}
	}
	if raw, ok, err := db.GetSetting(ctx, sqlite.SettingIntegrations); err != nil {
		return err
	} else if ok {
		var o integrationsOverlay
		if err = json.Unmarshal(raw, &o); err != nil {
			logger.Warn(fmt.Sprintf("settings %s: invalid JSON overlay: %v", sqlite.SettingIntegrations, err))
		} else {
			applyIntegrations(cfg, o)
		}
	}
	if raw, ok, err := db.GetSetting(ctx, settingWeb); err != nil {
		return err
	} else if ok {
		var o webOverlay
		if err = json.Unmarshal(raw, &o); err != nil {
			logger.Warn(fmt.Sprintf("settings %s: invalid JSON overlay: %v", settingWeb, err))
		} else {
			applyWeb(cfg, o)
		}
	}
	if n, err := normalizeAppURL(cfg.Web.AppURL); err != nil {
		warnOnce(fmt.Sprintf("settings web: WEB_APP_URL %q ignored: %v", cfg.Web.AppURL, err))
		cfg.Web.AppURL = ""
	} else {
		cfg.Web.AppURL = n
	}
	if err := NormalizeAppIntegrations(cfg); err != nil {
		return &InvalidConfigError{Err: err}
	}
	warnDerivedWatchMode(cfg)
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
	if o.MagnetTimeout != nil {
		cfg.Runner.MagnetTimeout = *o.MagnetTimeout
	}
	if o.MonitoredCategories != nil {
		cfg.Runner.AllowedCategories = *o.MonitoredCategories
	}
	if o.WatchTags != nil {
		cfg.Runner.WatchTags = *o.WatchTags
	} else if o.WatchTag != nil {
		cfg.Runner.WatchTags = watchTagsFromLegacy(*o.WatchTag)
	}
	if o.WatchMode != nil {
		cfg.Runner.WatchMode = *o.WatchMode
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
	if o.MirrorConfirm != nil {
		cfg.Runner.MirrorConfirm = *o.MirrorConfirm
	}
	if o.RunOnStart != nil {
		cfg.Runner.RunOnStart = *o.RunOnStart
	}
	if o.DeleteFilesCategories != nil {
		cfg.Manager.DeleteFilesCategories = *o.DeleteFilesCategories
	}
}

func applyTrackerOverlay(item *config.TrackerItem, o trackerItemOverlay) error {
	// omitempty выкидывает nil-указатели, и Unmarshal трогает только ключи из патча
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
	if o.Client != nil {
		switch client := config.NormalizeClient(*o.Client); client {
		case "":
			// пустая строка: клиент не меняем, ValidateIntegrationsPatch ее пропускает
		case "qbit", "transmission", "deluge":
			cfg.Manager.Client = client
		default:
			logger.Warn(fmt.Sprintf("settings %s: unknown client %q, keeping %q", sqlite.SettingIntegrations, *o.Client, cfg.Manager.Client))
		}
	}
	if o.Qbit.Host != nil {
		cfg.Manager.QbitTorrent.Host = *o.Qbit.Host
	}
	if o.Qbit.Username != nil {
		cfg.Manager.QbitTorrent.Username = *o.Qbit.Username
	}
	if o.Qbit.Password != nil {
		cfg.Manager.QbitTorrent.Password = *o.Qbit.Password
	}
	if o.Transmission.Host != nil {
		cfg.Manager.Transmission.Host = *o.Transmission.Host
	}
	if o.Transmission.Username != nil {
		cfg.Manager.Transmission.Username = *o.Transmission.Username
	}
	if o.Transmission.Password != nil {
		cfg.Manager.Transmission.Password = *o.Transmission.Password
	}
	if o.Deluge.Host != nil {
		cfg.Manager.Deluge.Host = *o.Deluge.Host
	}
	if o.Deluge.Password != nil {
		cfg.Manager.Deluge.Password = *o.Deluge.Password
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
	if v := o.Notifications.Telegram.Enabled; v != nil {
		cfg.Notify.TelegramDisabled = !*v
	}
	if v := o.Notifications.Webhook.Enabled; v != nil {
		cfg.Notify.WebhookDisabled = !*v
	}
	if v := o.Notifications.Email.Enabled; v != nil {
		cfg.Notify.EmailDisabled = !*v
	}
	if v := o.Notifications.Pushover.Enabled; v != nil {
		cfg.Notify.PushoverDisabled = !*v
	}
	if v := o.Notifications.Browser.Enabled; v != nil {
		cfg.Notify.BrowserDisabled = !*v
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
	if v := o.Notifications.Email.Host; v != nil {
		cfg.Notify.EmailHost = *v
	}
	if v := o.Notifications.Email.Port; v != nil {
		cfg.Notify.EmailPort = *v
	}
	if v := o.Notifications.Email.Security; v != nil {
		cfg.Notify.EmailSecurity = *v
	}
	if v := o.Notifications.Email.Username; v != nil {
		cfg.Notify.EmailUsername = *v
	}
	if v := o.Notifications.Email.Password; v != nil {
		cfg.Notify.EmailPassword = *v
	}
	if v := o.Notifications.Email.From; v != nil {
		cfg.Notify.EmailFrom = *v
	}
	if v := o.Notifications.Email.To; v != nil {
		cfg.Notify.EmailTo = *v
	}
	if v := o.Notifications.Pushover.AppToken; v != nil {
		cfg.Notify.PushoverToken = *v
	}
	if v := o.Notifications.Pushover.UserKey; v != nil {
		cfg.Notify.PushoverUser = *v
	}
	if o.Notifications.Enabled != nil {
		cfg.Notify.Enabled = *o.Notifications.Enabled
	}
	if v := o.Notifications.Language; v != nil {
		cfg.Notify.Language = *v
	}
	applyNotifyEvents(cfg, o)
	if o.Metrics.Token != nil {
		cfg.Metrics.Token = *o.Metrics.Token
	}
	if v := o.MirrorSource.Type; v != nil {
		cfg.MirrorSource.Type = *v
	}
	if v := o.MirrorSource.URL; v != nil {
		cfg.MirrorSource.URL = *v
	}
	if v := o.MirrorSource.Direct; v != nil {
		cfg.MirrorSource.Direct = *v
	}
}

func resolvedEvents(list []string) []string {
	if list == nil {
		return notify.DefaultEvents()
	}
	return list
}

func ScheduleJSON(cfg *config.App) json.RawMessage {
	s := struct {
		StartHour             int      `json:"start_hour"`
		ListRefreshPerDay     int      `json:"list_refresh_per_day"`
		ThresholdMonth        int      `json:"threshold_month"`
		ThresholdHalfYear     int      `json:"threshold_half_year"`
		ThresholdYear         int      `json:"threshold_year"`
		ErrorStreakThreshold  int      `json:"error_streak_threshold"`
		MagnetTimeout         int64    `json:"magnet_timeout"`
		DeleteEnabled         bool     `json:"delete_enabled"`
		DeleteFilesEnabled    bool     `json:"delete_files_enabled"`
		MirrorConfirm         bool     `json:"mirror_confirm"`
		RunOnStart            bool     `json:"run_on_start"`
		MonitoredCategories   []string `json:"monitored_categories"`
		DeleteFilesCategories []string `json:"delete_files_categories"`
		WatchTags             []string `json:"watch_tags"`
		WatchMode             string   `json:"watch_mode"`
		WatchModeStored       bool     `json:"watch_mode_stored"`
		TagsSupported         bool     `json:"tags_supported"`
		Timezone              string   `json:"timezone"`
	}{
		StartHour:            cfg.Runner.StartHour,
		ListRefreshPerDay:    cfg.Runner.ListRefreshPerDay,
		ThresholdMonth:       cfg.Runner.MonthThresholdDays,
		ThresholdHalfYear:    cfg.Runner.HalfYearThresholdDays,
		ThresholdYear:        cfg.Runner.YearThresholdDays,
		ErrorStreakThreshold: cfg.Runner.ErrorStreakThreshold,
		MagnetTimeout:        cfg.Runner.MagnetTimeout,
		MirrorConfirm:        cfg.Runner.MirrorConfirm,
		RunOnStart:           cfg.Runner.RunOnStart,
		MonitoredCategories:  cfg.Runner.AllowedCategories,
		WatchTags:            cfg.Runner.WatchTags,
		WatchMode:            cfg.Runner.EffectiveWatchMode(),
		WatchModeStored:      cfg.Runner.WatchMode == config.WatchModeCategories || cfg.Runner.WatchMode == config.WatchModeTags,
		TagsSupported:        config.ClientSupportsTags(cfg.Manager.Client),
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
	cfg = maskedCopy(cfg)
	s := struct {
		Client string `json:"client"`
		Qbit   struct {
			Host     string `json:"host"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"qbit"`
		Transmission struct {
			Host     string `json:"host"`
			Username string `json:"username"`
			Password string `json:"password"`
		} `json:"transmission"`
		Deluge struct {
			Host     string `json:"host"`
			Password string `json:"password"`
		} `json:"deluge"`
		Flaresolverr struct {
			URL       string `json:"url"`
			TimeoutMS int    `json:"timeout_ms"`
		} `json:"flaresolverr"`
		Proxy struct {
			DownloadProxy string `json:"download_proxy"`
		} `json:"proxy"`
		Notifications struct {
			Telegram struct {
				Enabled  bool   `json:"enabled"`
				BotToken string `json:"bot_token"`
				ChatID   string `json:"chat_id"`
			} `json:"telegram"`
			Webhook struct {
				Enabled bool   `json:"enabled"`
				URL     string `json:"url"`
			} `json:"webhook"`
			Email struct {
				Enabled  bool   `json:"enabled"`
				Host     string `json:"host"`
				Port     int    `json:"port"`
				Security string `json:"security"`
				Username string `json:"username"`
				Password string `json:"password"`
				From     string `json:"from"`
				To       string `json:"to"`
			} `json:"email"`
			Pushover struct {
				Enabled  bool   `json:"enabled"`
				AppToken string `json:"app_token"`
				UserKey  string `json:"user_key"`
			} `json:"pushover"`
			Browser struct {
				Enabled bool `json:"enabled"`
			} `json:"browser"`
			Enabled  bool     `json:"enabled"`
			Language string   `json:"language"`
			Events   []string `json:"events"`
		} `json:"notifications"`
		Metrics struct {
			Token string `json:"token"`
		} `json:"metrics"`
		MirrorSource struct {
			Type   string `json:"type"`
			URL    string `json:"url"`
			Direct bool   `json:"direct"`
		} `json:"mirror_source"`
	}{}
	s.Client = config.NormalizeClient(cfg.Manager.Client)
	s.Qbit.Host = cfg.Manager.QbitTorrent.Host
	s.Qbit.Username = cfg.Manager.QbitTorrent.Username
	s.Qbit.Password = cfg.Manager.QbitTorrent.Password
	s.Transmission.Host = cfg.Manager.Transmission.Host
	s.Transmission.Username = cfg.Manager.Transmission.Username
	s.Transmission.Password = cfg.Manager.Transmission.Password
	s.Deluge.Host = cfg.Manager.Deluge.Host
	s.Deluge.Password = cfg.Manager.Deluge.Password
	s.Flaresolverr.URL = cfg.Flaresolverr.Host
	s.Flaresolverr.TimeoutMS = cfg.Flaresolverr.MaxTimeout
	s.Proxy.DownloadProxy = cfg.Runner.DownloadProxy
	s.Notifications.Events = resolvedEvents(cfg.Notify.Events)
	s.Notifications.Telegram.Enabled = !cfg.Notify.TelegramDisabled
	s.Notifications.Webhook.Enabled = !cfg.Notify.WebhookDisabled
	s.Notifications.Email.Enabled = !cfg.Notify.EmailDisabled
	s.Notifications.Pushover.Enabled = !cfg.Notify.PushoverDisabled
	s.Notifications.Telegram.BotToken = cfg.Notify.TelegramToken
	s.Notifications.Telegram.ChatID = cfg.Notify.TelegramChatID
	s.Notifications.Webhook.URL = cfg.Notify.WebhookURL
	s.Notifications.Email.Host = cfg.Notify.EmailHost
	s.Notifications.Email.Port = cfg.Notify.EmailPort
	s.Notifications.Email.Security = cfg.Notify.EmailSecurity
	s.Notifications.Email.Username = cfg.Notify.EmailUsername
	s.Notifications.Email.Password = cfg.Notify.EmailPassword
	s.Notifications.Email.From = cfg.Notify.EmailFrom
	s.Notifications.Email.To = cfg.Notify.EmailTo
	s.Notifications.Pushover.AppToken = cfg.Notify.PushoverToken
	s.Notifications.Pushover.UserKey = cfg.Notify.PushoverUser
	s.Notifications.Browser.Enabled = !cfg.Notify.BrowserDisabled
	s.Notifications.Enabled = cfg.Notify.Enabled
	s.Notifications.Language = notify.NormalizeLanguage(cfg.Notify.Language)
	s.Metrics.Token = cfg.Metrics.Token
	s.MirrorSource.Type = cfg.MirrorSource.Type
	if s.MirrorSource.Type == "" {
		s.MirrorSource.Type = "github"
	}
	s.MirrorSource.URL = cfg.MirrorSource.URL
	s.MirrorSource.Direct = cfg.MirrorSource.Direct
	b, _ := json.Marshal(s)
	return b
}

func TrackersEffective(ctx context.Context, cfg *config.App, db settingsDB) (map[string]config.TrackerItem, error) {
	m := make(map[string]config.TrackerItem, len(cfg.Tracker))
	for key, it := range cfg.Tracker {
		m[key] = *it
	}
	if raw, ok, err := db.GetSetting(ctx, sqlite.SettingTrackers); err != nil {
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
		}
		if it.Retired == nil {
			it.Retired = []string{}
		}
		if it.Unconfirmed == nil {
			it.Unconfirmed = []string{}
		}
		m[k] = it
	}
	return m, nil
}

func mergePatch[T any](dst *T, patch T) error {
	// как в applyTrackerOverlay: omitempty отсекает пустые поля патча
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
	mergeMu.Lock()
	defer mergeMu.Unlock()
	var existing scheduleOverlay
	if raw, ok, err := db.GetSetting(ctx, sqlite.SettingSchedule); err != nil {
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
	return db.SaveSetting(ctx, sqlite.SettingSchedule, out)
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
	if raw, ok, err := db.GetSetting(ctx, sqlite.SettingTrackers); err != nil {
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
	return db.SaveSetting(ctx, sqlite.SettingTrackers, out)
}

func TrackersAdded(ctx context.Context, db settingsDB) ([]string, bool, error) {
	raw, ok, err := db.GetSetting(ctx, settingTrackersAdded)
	if err != nil || !ok {
		return nil, false, err
	}
	var keys []string
	if err = json.Unmarshal(raw, &keys); err != nil {
		logger.Warn(fmt.Sprintf("settings %s: invalid JSON: %v", settingTrackersAdded, err))
		return nil, false, nil
	}
	return keys, true, nil
}

func SaveTrackersAdded(ctx context.Context, db interface {
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}, keys []string) error {
	if keys == nil {
		keys = []string{}
	}
	out, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	return db.SaveSetting(ctx, settingTrackersAdded, out)
}

func DeleteTrackerKey(ctx context.Context, db interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}, key string) error {
	raw, ok, err := db.GetSetting(ctx, sqlite.SettingTrackers)
	if err != nil || !ok {
		return err
	}
	// RawMessage вместо типа overlay: документ запечатан целиком, неизвестные поля
	// и секреты других трекеров должны пережить перезапись без изменений
	var m map[string]json.RawMessage
	if err = json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if _, found := m[key]; !found {
		return nil
	}
	delete(m, key)
	out, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return db.SaveSetting(ctx, sqlite.SettingTrackers, out)
}

func MergeIntegrations(ctx context.Context, db interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}, patch json.RawMessage) error {
	overlay, err := IntegrationsPatchFromJSON(patch)
	if err != nil {
		return err
	}
	mergeMu.Lock()
	defer mergeMu.Unlock()
	var existing integrationsOverlay
	if raw, ok, err := db.GetSetting(ctx, sqlite.SettingIntegrations); err != nil {
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
	return db.SaveSetting(ctx, sqlite.SettingIntegrations, out)
}

type webOverlay struct {
	AllowedHosts *[]string `json:"allowed_hosts,omitempty"`
	AppURL       *string   `json:"app_url,omitempty"`
	Port         *int      `json:"port,omitempty"`
}

func applyWeb(cfg *config.App, o webOverlay) {
	// в Docker порт задается картой -p, сохраненный порт не применяется
	if o.Port != nil && cfg.Web.PortLocked {
		if _, p, err := net.SplitHostPort(cfg.Web.Listen); err == nil && p != strconv.Itoa(*o.Port) {
			warnOnce(fmt.Sprintf("settings web: stored web port %d ignored in Docker: map the host port with -p <host>:6410", *o.Port))
		}
	}
	if o.Port != nil && !cfg.Web.PortLocked {
		// хост берем из env/дефолта, в БД хранится только порт
		host, _, err := net.SplitHostPort(cfg.Web.Listen)
		if err != nil {
			host = ""
		}
		cfg.Web.Listen = net.JoinHostPort(host, strconv.Itoa(*o.Port))
	}
	if o.AllowedHosts != nil {
		cfg.Web.AllowedHosts = slices.Clone(*o.AllowedHosts)
	}
	if o.AppURL != nil {
		cfg.Web.AppURL = *o.AppURL
	}
}

func WebJSON(cfg *config.App) json.RawMessage {
	s := struct {
		AllowedHosts []string `json:"allowed_hosts"`
		Port         int      `json:"port"`
	}{AllowedHosts: slices.Clone(cfg.Web.AllowedHosts)}
	if s.AllowedHosts == nil {
		s.AllowedHosts = []string{}
	}
	if _, p, err := net.SplitHostPort(cfg.Web.Listen); err == nil {
		s.Port, _ = strconv.Atoi(p)
	}
	b, _ := json.Marshal(s)
	return b
}

func MergeWeb(ctx context.Context, db interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
	SaveSetting(ctx context.Context, key string, value json.RawMessage) error
}, patch json.RawMessage) error {
	overlay, err := WebPatchFromJSON(patch)
	if err != nil {
		return err
	}
	mergeMu.Lock()
	defer mergeMu.Unlock()
	var existing webOverlay
	if raw, ok, err := db.GetSetting(ctx, settingWeb); err != nil {
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
	return db.SaveSetting(ctx, settingWeb, out)
}

func warnDerivedWatchMode(cfg *config.App) {
	r := cfg.Runner
	if r.WatchMode == config.WatchModeCategories || r.WatchMode == config.WatchModeTags {
		return
	}
	if !config.ClientSupportsTags(cfg.Manager.Client) || len(r.AllowedCategories) == 0 || len(r.WatchTags) == 0 {
		return
	}
	warnOnce(fmt.Sprintf("settings schedule: watch_mode not saved and both monitored_categories %v and watch_tags %v are set: "+
		"watching by categories only, tags are ignored; save Settings to keep it or switch to tags", r.AllowedCategories, r.WatchTags))
}
