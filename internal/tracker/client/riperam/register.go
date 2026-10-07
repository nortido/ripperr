package riperam

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  9,
		Key: "riperam",
		Defaults: config.TrackerItem{
			Name:        "riperam",
			Domain:      "riperam.org",
			Publicity:   "private",
			LoginURL:    "https://riperam.org/ucp.php?mode=login",
			DownloadURL: "https://riperam.org/download/file.php?id=",
			HourLimit:   20,
			ProxyType:   0,
			Enabled:     false,
		},
		Login: &registry.LoginForm{
			UserField: "username",
			PassField: "password",
			Template:  "username=&password=&autologin=on&login=%D0%92%D1%85%D0%BE%D0%B4",
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
