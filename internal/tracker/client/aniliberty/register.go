package aniliberty

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  15,
		Key: "aniliberty",
		Defaults: config.TrackerItem{
			Name:        "aniliberty",
			Domain:      "aniliberty.top",
			Mirrors:     []string{"www.aniliberty.top"},
			Publicity:   "public",
			DownloadURL: "https://aniliberty.top/api/v1/anime/torrents/",
			HourLimit:   30,
			Enabled:     false,
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
