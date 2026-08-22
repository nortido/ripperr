package web

import (
	"encoding/json"

	"trackerr/internal/config"
	"trackerr/internal/settings"
	"trackerr/internal/tracker/registry"
)

func trackerName(id int) string {
	return registry.Key(id)
}

func trackerKnown(key string) bool {
	_, ok := registry.ByKey(key)
	return ok
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
