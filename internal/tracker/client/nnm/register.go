package nnm

import (
	"trackerr/internal/config"
	trackerModel "trackerr/internal/model/tracker"
	"trackerr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  6,
		Key: "nnm",
		Defaults: config.TrackerItem{
			Name:        "nnm",
			Domain:      "nnmclub.to",
			Publicity:   "private",
			LoginURL:    "https://nnmclub.to/forum/login.php",
			DownloadURL: "https://nnmclub.to/forum/download.php?id=",
			HourLimit:   25,
			ProxyType:   1,
			Enabled:     true,
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
