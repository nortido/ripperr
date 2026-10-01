package tracker

import (
	"ripperr/internal/config"
	"ripperr/internal/helper"
	proxyModel "ripperr/internal/model/proxy"
	trackerModel "ripperr/internal/model/tracker"
)

func trackerFromItem(it config.TrackerItem) trackerModel.Tracker {
	return trackerModel.Tracker{
		Name: it.Name, Domain: it.Domain, ActiveDomain: it.ActiveDomain, Mirrors: it.Mirrors,
		Credentials: it.Credentials, ProxyType: proxyModel.ProxyType(it.ProxyType),
		Publicity: it.Publicity, LoginURL: it.LoginURL, DownloadURL: it.DownloadURL,
		HourLimit: it.HourLimit, Cookies: helper.ParseCookies(it.Cookies), Enabled: it.Enabled,
	}
}

func ConfigFromApp(cfg config.App) trackerModel.TrackerConfig {
	out := make(trackerModel.TrackerConfig, len(cfg.Tracker))
	for key, it := range cfg.Tracker {
		out[key] = trackerFromItem(*it)
	}
	return out
}
