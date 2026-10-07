package selezen

import (
	"ripperr/internal/config"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/registry"
)

func init() {
	registry.Register(registry.Def{
		ID:  13,
		Key: "selezen",
		Defaults: config.TrackerItem{
			Name:        "selezen",
			Domain:      "selezen.top",
			Mirrors:     []string{"www.selezen.club"},
			Publicity:   "private",
			LoginURL:    "https://selezen.top/index.php",
			DownloadURL: "",
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
