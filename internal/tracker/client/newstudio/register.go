package newstudio

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  16,
		Key: "newstudio",
		Defaults: config.TrackerItem{
			Name:        "newstudio",
			Domain:      "newstudio.tv",
			Publicity:   "public",
			LoginURL:    "https://newstudio.tv/login.php",
			DownloadURL: "https://newstudio.tv/download.php?id=",
			HourLimit:   20,
			ProxyType:   0,
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
