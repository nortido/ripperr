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
		Credentials: formcreds.Expand(it.Credentials, it.Login, it.Password), ProxyType: trackerProxyType(it.ProxyType),
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

func trackerProxyType(v int) proxyModel.ProxyType {
	// режим FlareSolverr сначала идет напрямую с отпечатком Chrome, 2 остался от отдельного пункта
	if v == int(proxyModel.TypeFlare) || v == int(proxyModel.TypeTLS) {
		return proxyModel.TypeTLS
	}
	return proxyModel.ProxyType(v)
}
