package teamhd

import (
	"trackerr/internal/config"
	trackerModel "trackerr/internal/model/tracker"
	"trackerr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  5,
		Key: "teamhd",
		Defaults: config.TrackerItem{
			Name:        "teamhd",
			Domain:      "teamhd.org",
			Publicity:   "private",
			DownloadURL: "https://teamhd.org/download.php?id=",
			HourLimit:   25,
			Enabled:     true,
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
