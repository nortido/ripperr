package rutor

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  3,
		Key: "rutor",
		Defaults: config.TrackerItem{
			Name:        "rutor",
			Domain:      "rutor.is",
			Mirrors:     []string{"rutor.info"},
			Publicity:   "public",
			DownloadURL: "https://d.rutor.info/download/",
			HourLimit:   30,
			Enabled:     true,
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
