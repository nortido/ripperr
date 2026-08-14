package tracker

import (
	"trackerr/internal/config"
	"trackerr/internal/helper"
	proxyModel "trackerr/internal/model/proxy"
	trackerModel "trackerr/internal/model/tracker"
)

func trackerFromItem(it config.TrackerItem) trackerModel.Tracker {
	return trackerModel.Tracker{
		Name: it.Name, Domain: it.Domain, ActiveDomain: it.ActiveDomain, Mirrors: it.Mirrors,
		Credentials: it.Credentials, ProxyType: proxyModel.ProxyType(it.ProxyType),
		Publicity: it.Publicity, LoginURL: it.LoginURL, DownloadURL: it.DownloadURL,
		HourLimit: it.HourLimit, Cookies: helper.ParseCookies(it.Cookies), Enabled: it.Enabled,
	}
}

func ConfigFromApp(cfg config.App) *trackerModel.TrackerConfig {
	return &trackerModel.TrackerConfig{
		Rutracker: trackerFromItem(cfg.Tracker.Rutracker),
		Kinozal:   trackerFromItem(cfg.Tracker.Kinozal),
		Rutor:     trackerFromItem(cfg.Tracker.Rutor),
		TeamHD:    trackerFromItem(cfg.Tracker.TeamHD),
		NNM:       trackerFromItem(cfg.Tracker.NNM),
	}
}
