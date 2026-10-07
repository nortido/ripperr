package animelayer

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  12,
		Key: "animelayer",
		Defaults: config.TrackerItem{
			Name:        "animelayer",
			Domain:      "animelayer.ru",
			Publicity:   "private",
			LoginURL:    "https://animelayer.ru/auth/login/",
			DownloadURL: "https://animelayer.ru/torrent/",
			HourLimit:   20,
			ProxyType:   0,
			Enabled:     false,
		},
		Login: &registry.LoginForm{
			UserField: "login",
			PassField: "password",
			Template:  "login=&password=",
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
