package kinozal

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  2,
		Key: "kinozal",
		Defaults: config.TrackerItem{
			Name:        "kinozal",
			Domain:      "kinozal.tv",
			Publicity:   "private",
			LoginURL:    "https://kinozal.tv/takelogin.php",
			DownloadURL: "https://kinozal.tv/download.php?id=",
			HourLimit:   30,
			ProxyType:   1,
			Enabled:     true,
		},
		Login: &registry.LoginForm{
			UserField: "username",
			PassField: "password",
			Template:  "username=&password=&to=%2F",
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
