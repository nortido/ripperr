package settings

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/notify"
)

var allowedRefreshPerDay = config.AllowedRefreshPerDay

type Schedule struct {
	StartHour             int      `json:"start_hour"`
	ListRefreshPerDay     int      `json:"list_refresh_per_day"`
	ThresholdMonth        int      `json:"threshold_month"`
	ThresholdHalfYear     int      `json:"threshold_half_year"`
	ThresholdYear         int      `json:"threshold_year"`
	ErrorStreakThreshold  int      `json:"error_streak_threshold"`
	MagnetTimeout         int64    `json:"magnet_timeout"`
	MonitoredCategories   []string `json:"monitored_categories"`
	DeleteFilesCategories []string `json:"delete_files_categories"`
}

func normalizeStringList(ss []string) []string {
	if ss == nil {
		return nil
	}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func ValidateSchedule(s *Schedule) error {
	if s.StartHour < 0 || s.StartHour > 23 {
		return fmt.Errorf("start_hour must be 0..23")
	}
	if !slices.Contains(allowedRefreshPerDay, s.ListRefreshPerDay) {
		return fmt.Errorf("list_refresh_per_day must be one of %v", allowedRefreshPerDay)
	}
	if s.ThresholdMonth < 1 || s.ThresholdHalfYear < 1 || s.ThresholdYear < 1 {
		return fmt.Errorf("thresholds must be >= 1")
	}
	if s.ErrorStreakThreshold != 0 && s.ErrorStreakThreshold < 1 {
		return fmt.Errorf("error_streak_threshold must be >= 1")
	}
	// границы как в ValidateSchedulePatch. 0 тут уже не значит "не задано":
	// отсутствующий ключ превратился в 60 еще до этой функции
	if s.MagnetTimeout < 5 || s.MagnetTimeout > 600 {
		return fmt.Errorf("magnet_timeout must be 5..600")
	}
	s.MonitoredCategories = normalizeStringList(s.MonitoredCategories)
	s.DeleteFilesCategories = normalizeStringList(s.DeleteFilesCategories)
	return nil
}

func SchedulePatchFromJSON(raw json.RawMessage) (scheduleOverlay, error) {
	var o scheduleOverlay
	if err := json.Unmarshal(raw, &o); err != nil {
		return o, err
	}
	if err := ValidateSchedulePatch(o); err != nil {
		return o, err
	}
	if o.MonitoredCategories != nil {
		n := normalizeStringList(*o.MonitoredCategories)
		o.MonitoredCategories = &n
	}
	if o.DeleteFilesCategories != nil {
		n := normalizeStringList(*o.DeleteFilesCategories)
		o.DeleteFilesCategories = &n
	}
	if o.WatchTags != nil {
		n, err := normalizeWatchTagList(*o.WatchTags)
		if err != nil {
			return o, err
		}
		o.WatchTags = &n
	} else if o.WatchTag != nil {
		n, err := normalizeWatchTagList(watchTagsFromLegacy(*o.WatchTag))
		if err != nil {
			return o, err
		}
		o.WatchTags = &n
		o.WatchTag = nil
	}
	if o.Timezone != nil {
		tz := strings.TrimSpace(*o.Timezone)
		if tz != "" {
			if _, err := time.LoadLocation(tz); err != nil {
				return o, fmt.Errorf("unknown timezone: %s", tz)
			}
		}
		o.Timezone = &tz
	}
	return o, nil
}

func ValidateSchedulePatch(o scheduleOverlay) error {
	if o.WatchMode != nil && *o.WatchMode != config.WatchModeCategories && *o.WatchMode != config.WatchModeTags {
		return fmt.Errorf("watch_mode must be categories or tags")
	}
	// патч без расписания (напр. только mirror_confirm): проверять нечего
	if o.StartHour == nil && o.ListRefreshPerDay == nil && o.ThresholdMonth == nil && o.ThresholdHalfYear == nil &&
		o.ThresholdYear == nil && o.ErrorStreakThreshold == nil && o.MagnetTimeout == nil {
		return nil
	}
	if o.StartHour == nil {
		return fmt.Errorf("start_hour is required")
	}
	if o.ListRefreshPerDay == nil {
		return fmt.Errorf("list_refresh_per_day is required")
	}
	if o.ThresholdMonth == nil {
		return fmt.Errorf("threshold_month is required")
	}
	if o.ThresholdHalfYear == nil {
		return fmt.Errorf("threshold_half_year is required")
	}
	if o.ThresholdYear == nil {
		return fmt.Errorf("threshold_year is required")
	}
	// необязательное: в документах, сохраненных раньше, этого поля нет
	errorStreakThreshold := 3
	if o.ErrorStreakThreshold != nil {
		if *o.ErrorStreakThreshold < 1 {
			return fmt.Errorf("error_streak_threshold must be >= 1")
		}
		errorStreakThreshold = *o.ErrorStreakThreshold
	}
	// тоже может отсутствовать в старых документах
	var magnetTimeout int64 = 60
	if o.MagnetTimeout != nil {
		if *o.MagnetTimeout < 5 || *o.MagnetTimeout > 600 {
			return fmt.Errorf("magnet_timeout must be 5..600")
		}
		magnetTimeout = *o.MagnetTimeout
	}
	s := Schedule{
		StartHour:            *o.StartHour,
		ListRefreshPerDay:    *o.ListRefreshPerDay,
		ThresholdMonth:       *o.ThresholdMonth,
		ThresholdHalfYear:    *o.ThresholdHalfYear,
		ThresholdYear:        *o.ThresholdYear,
		ErrorStreakThreshold: errorStreakThreshold,
		MagnetTimeout:        magnetTimeout,
	}
	if o.MonitoredCategories != nil {
		s.MonitoredCategories = *o.MonitoredCategories
	}
	if o.DeleteFilesCategories != nil {
		s.DeleteFilesCategories = *o.DeleteFilesCategories
	}
	return ValidateSchedule(&s)
}

func TrackerPatchFromJSON(raw json.RawMessage) (trackerItemOverlay, error) {
	var o trackerItemOverlay
	if err := json.Unmarshal(raw, &o); err != nil {
		return o, err
	}
	if err := ValidateTrackerPatch(o); err != nil {
		return o, err
	}
	for _, list := range []*[]string{o.Mirrors, o.Retired, o.Unconfirmed} {
		if list == nil {
			continue
		}
		n, err := normalizeMirrorList(*list)
		if err != nil {
			return o, err
		}
		*list = n
	}
	if o.ActiveDomain != nil {
		d := strings.TrimSpace(*o.ActiveDomain)
		o.ActiveDomain = &d
	}
	return o, nil
}

func ValidateTrackerPatch(o trackerItemOverlay) error {
	if o.ProxyType != nil && *o.ProxyType != 0 && *o.ProxyType != 1 {
		return fmt.Errorf("proxy_type must be 0 or 1")
	}
	if o.HourLimit != nil && *o.HourLimit < 1 {
		return fmt.Errorf("hour_limit must be >= 1")
	}
	if o.Mirrors != nil {
		if _, err := normalizeMirrorList(*o.Mirrors); err != nil {
			return err
		}
	}
	if o.Domain != nil {
		if err := validateTrackerHostname(strings.TrimSpace(*o.Domain), "domain"); err != nil {
			return err
		}
	}
	if o.ActiveDomain != nil {
		d := strings.TrimSpace(*o.ActiveDomain)
		if d != "" {
			if err := validateTrackerHostname(d, "active_domain"); err != nil {
				return err
			}
		}
	}
	if o.LoginURL != nil {
		if err := validateTrackerHTTPURL(*o.LoginURL, "login_url"); err != nil {
			return err
		}
	}
	if o.DownloadURL != nil {
		if err := validateTrackerHTTPURL(*o.DownloadURL, "download_url"); err != nil {
			return err
		}
	}
	return nil
}

func validateTrackerHostname(s, label string) error {
	if s == "" {
		return fmt.Errorf("%s must not be empty", label)
	}
	if strings.ContainsAny(s, " \t\n\r/") {
		return fmt.Errorf("%s must not contain spaces or slashes: %q", label, s)
	}
	if strings.Contains(s, "://") {
		return fmt.Errorf("%s must not include a URL scheme: %q", label, s)
	}
	host, port, hasPort := strings.Cut(s, ":")
	if hasPort {
		if host == "" || port == "" {
			return fmt.Errorf("%s has invalid host:port: %q", label, s)
		}
		for _, c := range port {
			if c < '0' || c > '9' {
				return fmt.Errorf("%s has invalid port: %q", label, s)
			}
		}
		if len(port) > 5 {
			return fmt.Errorf("%s has invalid port: %q", label, s)
		}
	} else {
		host = s
	}
	for i, c := range host {
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.'
		if !ok {
			return fmt.Errorf("%s contains invalid hostname characters: %q", label, s)
		}
		if c == '-' && (i == 0 || i == len(host)-1 || host[i-1] == '.' || (i+1 < len(host) && host[i+1] == '.')) {
			return fmt.Errorf("%s has invalid hostname: %q", label, s)
		}
		if c == '.' && (i == 0 || i == len(host)-1) {
			return fmt.Errorf("%s has invalid hostname: %q", label, s)
		}
	}
	return nil
}

func validateTrackerHTTPURL(raw, label string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.ContainsAny(raw, " \t\n\r") {
		return fmt.Errorf("%s must not contain spaces", label)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("%s must use http or https scheme", label)
	}
	if u.Host == "" {
		return fmt.Errorf("%s: invalid url: missing host", label)
	}
	return nil
}

func normalizeMirrorList(ss []string) ([]string, error) {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if err := validateTrackerHostname(s, "mirror domain"); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func watchTagsFromLegacy(tag string) []string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return []string{}
	}
	return []string{tag}
}

func normalizeWatchTagList(ss []string) ([]string, error) {
	if ss == nil {
		return []string{}, nil
	}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("watch_tags elements must be non-empty")
		}
		if strings.Contains(s, ",") {
			return nil, fmt.Errorf("watch_tags element must not contain commas: %q", s)
		}
		out = append(out, s)
	}
	return out, nil
}

func WatchTagsFromEnv(tag string) []string {
	return watchTagsFromLegacy(tag)
}

func validateEvents(field string, events *[]string) error {
	if events == nil {
		return nil
	}
	for _, kind := range *events {
		if _, ok := notify.CanonicalEvent(kind); !ok {
			return fmt.Errorf("%s: unknown event %q", field, kind)
		}
	}
	return nil
}

func IntegrationsPatchFromJSON(raw json.RawMessage) (integrationsOverlay, error) {
	var o integrationsOverlay
	if err := json.Unmarshal(raw, &o); err != nil {
		return o, err
	}
	if err := ValidateIntegrationsPatch(o); err != nil {
		return o, err
	}
	if err := normalizeIntegrationsPatch(&o); err != nil {
		return o, err
	}
	for _, events := range []*[]string{
		o.Notifications.Events,
		o.Notifications.Telegram.Events,
		o.Notifications.Webhook.Events,
		o.Notifications.Email.Events,
		o.Notifications.Pushover.Events,
		o.Notifications.Browser.Events,
	} {
		if events != nil {
			*events = notify.NormalizeEvents(*events)
		}
	}
	return o, nil
}

func ValidateIntegrationsPatch(o integrationsOverlay) error {
	// пустая строка: клиент не меняем. Сравниваем после нормализации, иначе "Transmission"
	// из UI получит 400, хотя applyIntegrations его примет. Там же подстраховка для старых баз
	if o.Client != nil {
		if client := config.NormalizeClient(*o.Client); client != "" && client != "qbit" && client != "transmission" && client != "deluge" && client != "downloadstation" {
			return fmt.Errorf("client must be qbit, transmission, deluge or downloadstation")
		}
	}
	if o.Flaresolverr.TimeoutMS != nil && *o.Flaresolverr.TimeoutMS < 1 {
		return fmt.Errorf("flaresolverr.timeout_ms must be >= 1")
	}
	// в отличие от client, пустой тип не пропускаем: UI всегда шлет выбранное значение
	if t := o.MirrorSource.Type; t != nil && *t != "github" && *t != "jsdelivr" && *t != "custom" {
		return fmt.Errorf("mirror_source.type must be github, jsdelivr or custom")
	}
	if err := validateEvents("notifications.events", o.Notifications.Events); err != nil {
		return err
	}
	if err := validateEvents("notifications.telegram.events", o.Notifications.Telegram.Events); err != nil {
		return err
	}
	if err := validateEvents("notifications.webhook.events", o.Notifications.Webhook.Events); err != nil {
		return err
	}
	if err := validateEvents("notifications.email.events", o.Notifications.Email.Events); err != nil {
		return err
	}
	if err := validateEvents("notifications.pushover.events", o.Notifications.Pushover.Events); err != nil {
		return err
	}
	if err := validateEvents("notifications.browser.events", o.Notifications.Browser.Events); err != nil {
		return err
	}
	if l := o.Notifications.Language; l != nil && *l != "" && *l != notify.LangEN && *l != notify.LangRU {
		return fmt.Errorf("notifications.language must be en or ru")
	}
	return validateEmail(o)
}

func validateEmail(o integrationsOverlay) error {
	e := o.Notifications.Email
	if e.Port != nil && (*e.Port < 0 || *e.Port > 65535) {
		return fmt.Errorf("notifications.email.port must be 0..65535")
	}
	if e.Security != nil {
		switch *e.Security {
		case "", "starttls", "tls", "none":
		default:
			return fmt.Errorf("notifications.email.security must be starttls, tls, or none")
		}
	}
	if e.From != nil && strings.TrimSpace(*e.From) != "" {
		if _, err := mail.ParseAddress(*e.From); err != nil {
			return fmt.Errorf("notifications.email.from: %w", err)
		}
	}
	if e.To != nil && strings.TrimSpace(*e.To) != "" {
		if _, err := mail.ParseAddressList(*e.To); err != nil {
			return fmt.Errorf("notifications.email.to: %w", err)
		}
	}
	return nil
}

func WebPatchFromJSON(raw json.RawMessage) (webOverlay, error) {
	var o webOverlay
	if err := json.Unmarshal(raw, &o); err != nil {
		return o, err
	}
	if o.Port != nil && (*o.Port < 1 || *o.Port > 65535) {
		return o, fmt.Errorf("port must be 1..65535")
	}
	if o.AllowedHosts != nil {
		n, err := normalizeAllowedHosts(*o.AllowedHosts)
		if err != nil {
			return o, err
		}
		o.AllowedHosts = &n
	}
	if o.AppURL != nil {
		n, err := normalizeAppURL(*o.AppURL)
		if err != nil {
			return o, err
		}
		o.AppURL = &n
	}
	return o, nil
}

func normalizeAllowedHosts(ss []string) ([]string, error) {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), ".")
		if h == "" {
			continue
		}
		if strings.ContainsAny(h, "/@* \t\r\n") {
			return nil, fmt.Errorf("allowed_hosts: invalid entry %q", s)
		}
		if strings.Contains(h, ":") {
			if _, err := netip.ParseAddr(h); err != nil {
				return nil, fmt.Errorf("allowed_hosts: invalid entry %q", s)
			}
		}
		if !slices.Contains(out, h) {
			out = append(out, h)
		}
	}
	return out, nil
}

func normalizeAppURL(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("app_url: need an http(s) address with a host, for example https://ripperr.example.com")
	}
	scheme := strings.ToLower(u.Scheme)
	if (scheme != "http" && scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("app_url: need an http(s) address with a host, for example https://ripperr.example.com")
	}
	if u.User != nil {
		return "", fmt.Errorf("app_url: login and password are not allowed")
	}
	// ? и # ловим по строке: url.Parse молча сбрасывает пустые запрос и фрагмент
	if strings.ContainsAny(s, "?#") {
		return "", fmt.Errorf("app_url: query and fragment are not allowed")
	}
	return scheme + "://" + strings.ToLower(u.Host) + strings.TrimRight(u.EscapedPath(), "/"), nil
}
