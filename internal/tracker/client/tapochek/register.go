package tapochek

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  8,
		Key: "tapochek",
		Defaults: config.TrackerItem{
			Name:        "tapochek",
			Domain:      "tapochek.net",
			Publicity:   "private",
			LoginURL:    "https://tapochek.net/login.php",
			DownloadURL: "https://tapochek.net/download.php?id=",
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
