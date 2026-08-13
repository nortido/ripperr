package web

import (
	"encoding/json"

	"trackerr/internal/config"
	"trackerr/internal/settings"
	"trackerr/internal/tracker"
)

var (
	trackerKeyByID = map[int]string{
		tracker.RutrackerID: "rutracker",
		tracker.KinozalID:   "kinozal",
		tracker.RutorID:     "rutor",
		tracker.TeamhdID:    "teamhd",
		tracker.NnmID:       "nnm",
	}
	trackerIDByKey = map[string]int{
		"rutracker": tracker.RutrackerID,
		"kinozal":   tracker.KinozalID,
		"rutor":     tracker.RutorID,
		"teamhd":    tracker.TeamhdID,
		"nnm":       tracker.NnmID,
	}
)

func trackerName(id int) string {
	if k, ok := trackerKeyByID[id]; ok {
		return k
	}
	return ""
}

func trackerProxyType(cfg config.App, key string) int {
	if it := cfg.Tracker.ByKey(key); it != nil {
		return it.ProxyType
	}
	return 0
}

func effectiveTrackerProxyType(cfg config.App, key string, configPatch json.RawMessage) int {
	if len(configPatch) > 0 && string(configPatch) != "null" {
		if overlay, err := settings.TrackerPatchFromJSON(configPatch); err == nil && overlay.ProxyType != nil {
			return *overlay.ProxyType
		}
	}
	return trackerProxyType(cfg, key)
}
