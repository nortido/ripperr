package toloka

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  7,
		Key: "toloka",
		Defaults: config.TrackerItem{
			Name:        "toloka",
			Domain:      "toloka.to",
			Publicity:   "private",
			LoginURL:    "https://toloka.to/login.php",
			DownloadURL: "https://toloka.to/download.php?id=",
			HourLimit:   20,
			ProxyType:   0,
			Enabled:     false,
		},
		Login: &registry.LoginForm{
			UserField: "username",
			PassField: "password",
			Template:  "username=&password=&autologin=on&ssl=on&redirect=&login=%D0%92%D1%85%D1%96%D0%B4",
		},
		New: func(t trackerModel.Tracker) trackerModel.ITracker { return New(t) },
	})
}
