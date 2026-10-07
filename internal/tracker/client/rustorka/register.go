package rustorka

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  14,
		Key: "rustorka",
		Defaults: config.TrackerItem{
			Name:        "rustorka",
			Domain:      "rustorka.com",
			Publicity:   "private",
			DownloadURL: "https://rustorka.com/forum/download.php?id=",
			HourLimit:   15,
			Enabled:     false,
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
