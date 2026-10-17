package tracker

import (
	"ripperr/internal/config"
	"ripperr/internal/helper"
	"ripperr/internal/helper/formcreds"
	proxyModel "ripperr/internal/model/proxy"
	trackerModel "ripperr/internal/model/tracker"
)

func trackerFromItem(it config.TrackerItem) trackerModel.Tracker {
	return trackerModel.Tracker{
		Name: it.Name, Domain: it.Domain, ActiveDomain: it.ActiveDomain, Mirrors: it.Mirrors, Retired: it.Retired,
		Credentials: formcreds.Expand(it.Credentials, it.Login, it.Password), ProxyType: proxyModel.ProxyType(it.ProxyType),
		Publicity: it.Publicity, LoginURL: it.LoginURL, DownloadURL: it.DownloadURL,
		HourLimit: it.HourLimit, Cookies: helper.ParseCookies(it.Cookies), Enabled: it.Enabled,
	}
}

func ConfigFromApp(cfg config.App) trackerModel.TrackerConfig {
	out := make(trackerModel.TrackerConfig, len(cfg.Tracker))
	for key, it := range cfg.Tracker {
		t := trackerFromItem(*it)
		// выключенный тоггл значит "неподтвержденных нет": охрана ротации не включается
		if cfg.Runner.MirrorConfirm {
			t.Unconfirmed = it.Unconfirmed
		}
		out[key] = t
	}
	return out
}
