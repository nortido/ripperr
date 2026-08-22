package settings

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"trackerr/internal/config"
)

var allowedRefreshPerDay = []int{1, 2, 3, 4, 6, 8, 12, 24}

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
	// границы совпадают с ValidateSchedulePatch: 0 больше не особый случай
	// "не задано", там это уже разрешено на уровне отсутствия ключа в патче
	// (magnetTimeout по умолчанию 60 до вызова этой функции)
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
	// необязательное: отсутствует в документах, сохранённых до появления поля
	errorStreakThreshold := 3
	if o.ErrorStreakThreshold != nil {
		if *o.ErrorStreakThreshold < 1 {
			return fmt.Errorf("error_streak_threshold must be >= 1")
		}
		errorStreakThreshold = *o.ErrorStreakThreshold
	}
	// необязательное: отсутствует в документах, сохранённых до появления поля
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
	if o.Mirrors != nil {
		n, err := normalizeMirrorList(*o.Mirrors)
		if err != nil {
			return o, err
		}
		o.Mirrors = &n
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
	return o, nil
}

func ValidateIntegrationsPatch(o integrationsOverlay) error {
	// пустая строка = не менять активный клиент; неизвестное значение отклоняем на
	// границе, а не молча роняем в applyIntegrations (там это вторая линия обороны
	// для старых баз, куда битое значение попало в обход этой проверки). Сравниваем
	// нормализованное значение, иначе "Transmission" из UI/старого патча 400-ится
	// здесь же, хотя applyIntegrations его бы принял.
	if o.Client != nil {
		if client := config.NormalizeClient(*o.Client); client != "" && client != "qbit" && client != "transmission" {
			return fmt.Errorf("client must be qbit or transmission")
		}
	}
	if o.Flaresolverr.TimeoutMS != nil && *o.Flaresolverr.TimeoutMS < 1 {
		return fmt.Errorf("flaresolverr.timeout_ms must be >= 1")
	}
	return nil
}
