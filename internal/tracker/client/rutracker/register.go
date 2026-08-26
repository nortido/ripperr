package rutracker

import (
	"trackerr/internal/config"
	trackerModel "trackerr/internal/model/tracker"
	"trackerr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  1,
		Key: "rutracker",
		Defaults: config.TrackerItem{
			Name:        "rutracker",
			Domain:      "rutracker.org",
			Publicity:   "private",
			LoginURL:    "https://rutracker.org/forum/login.php",
			DownloadURL: "https://rutracker.org/forum/dl.php?t=",
			HourLimit:   20,
			ProxyType:   1,
			Enabled:     true,
		},
		Login: &registry.LoginForm{
			UserField: "login_username",
			PassField: "login_password",
			Template:  "login_username=&login_password=&login=%D0%B2%D1%85%D0%BE%D0%B4",
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
