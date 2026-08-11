package settings

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

var allowedRefreshPerDay = []int{1, 2, 3, 4, 6, 8, 12, 24}

type Schedule struct {
	StartHour             int      `json:"start_hour"`
	ListRefreshPerDay     int      `json:"list_refresh_per_day"`
	ThresholdMonth        int      `json:"threshold_month"`
	ThresholdHalfYear     int      `json:"threshold_half_year"`
	ThresholdYear         int      `json:"threshold_year"`
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
	s.MonitoredCategories = normalizeStringList(s.MonitoredCategories)
	s.DeleteFilesCategories = normalizeStringList(s.DeleteFilesCategories)
	return nil
}

func ScheduleFromJSON(raw json.RawMessage) (*Schedule, error) {
	var s Schedule
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	if err := ValidateSchedule(&s); err != nil {
		return nil, err
	}
	return &s, nil
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
	s := Schedule{
		StartHour:         *o.StartHour,
		ListRefreshPerDay: *o.ListRefreshPerDay,
		ThresholdMonth:    *o.ThresholdMonth,
		ThresholdHalfYear: *o.ThresholdHalfYear,
		ThresholdYear:     *o.ThresholdYear,
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
	if o.ActiveDomain != nil {
		d := strings.TrimSpace(*o.ActiveDomain)
		if d != "" && strings.ContainsAny(d, " /") {
			return fmt.Errorf("active_domain must not contain spaces or slashes: %q", d)
		}
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
		if strings.ContainsAny(s, " /") {
			return nil, fmt.Errorf("mirror domain must not contain spaces or slashes: %q", s)
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

// WatchTagsFromEnv converts RUNNER_TAG env value to the effective watch tag list.
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
	if o.Flaresolverr.TimeoutMS != nil && *o.Flaresolverr.TimeoutMS < 1 {
		return fmt.Errorf("flaresolverr.timeout_ms must be >= 1")
	}
	return nil
}
