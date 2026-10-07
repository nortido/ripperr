package booktracker

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  10,
		Key: "booktracker",
		Defaults: config.TrackerItem{
			Name:        "booktracker",
			Domain:      "booktracker.org",
			Publicity:   "private",
			LoginURL:    "https://booktracker.org/login.php",
			DownloadURL: "https://booktracker.org/download.php?id=",
			HourLimit:   20,
			Enabled:     false,
		},
		Login: &registry.LoginForm{
			UserField: "login_username",
			PassField: "login_password",
			Template:  "login_username=&login_password=&autologin=1&redirect=index.php&login=1",
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
