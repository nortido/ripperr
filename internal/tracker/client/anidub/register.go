package anidub

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  11,
		Key: "anidub",
		Defaults: config.TrackerItem{
			Name:        "anidub",
			Domain:      "tr.anidub.com",
			Publicity:   "public",
			LoginURL:    "https://tr.anidub.com/index.php",
			DownloadURL: "https://tr.anidub.com/engine/download.php?id=",
			HourLimit:   20,
			ProxyType:   0,
			Enabled:     false,
		},
		Login: &registry.LoginForm{
			UserField: "login_name",
			PassField: "login_password",
			Template:  "login_name=&login_password=&login=submit",
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
