package tracker

import (
	"trackerr/internal/config"
	"trackerr/internal/helper"
	proxyModel "trackerr/internal/model/proxy"
	trackerModel "trackerr/internal/model/tracker"
)

// ConfigFromApp builds tracker config from effective app settings.
func ConfigFromApp(cfg config.App) *trackerModel.TrackerConfig {
	return &trackerModel.TrackerConfig{
		Rutracker: trackerModel.Tracker{
			Name:         cfg.Tracker.Rutracker.Name,
			Domain:       cfg.Tracker.Rutracker.Domain,
			ActiveDomain: cfg.Tracker.Rutracker.ActiveDomain,
			Mirrors:      cfg.Tracker.Rutracker.Mirrors,
			Credentials:  cfg.Tracker.Rutracker.Credentials,
			ProxyType:    proxyModel.ProxyType(cfg.Tracker.Rutracker.ProxyType),
			Publicity:    cfg.Tracker.Rutracker.Publicity,
			LoginURL:     cfg.Tracker.Rutracker.LoginURL,
			DownloadURL:  cfg.Tracker.Rutracker.DownloadURL,
			HourLimit:    cfg.Tracker.Rutracker.HourLimit,
			Enabled:      cfg.Tracker.Rutracker.Enabled,
		},
		Kinozal: trackerModel.Tracker{
			Name:         cfg.Tracker.Kinozal.Name,
			Domain:       cfg.Tracker.Kinozal.Domain,
			ActiveDomain: cfg.Tracker.Kinozal.ActiveDomain,
			Mirrors:      cfg.Tracker.Kinozal.Mirrors,
			Credentials:  cfg.Tracker.Kinozal.Credentials,
			ProxyType:    proxyModel.ProxyType(cfg.Tracker.Kinozal.ProxyType),
			Publicity:    cfg.Tracker.Kinozal.Publicity,
			LoginURL:     cfg.Tracker.Kinozal.LoginURL,
			DownloadURL:  cfg.Tracker.Kinozal.DownloadURL,
			HourLimit:    cfg.Tracker.Kinozal.HourLimit,
			Cookies:      helper.ParseCookies(cfg.Tracker.Kinozal.Cookies),
			Enabled:      cfg.Tracker.Kinozal.Enabled,
		},
		Rutor: trackerModel.Tracker{
			Name:         cfg.Tracker.Rutor.Name,
			Domain:       cfg.Tracker.Rutor.Domain,
			ActiveDomain: cfg.Tracker.Rutor.ActiveDomain,
			Mirrors:      cfg.Tracker.Rutor.Mirrors,
			Publicity:    cfg.Tracker.Rutor.Publicity,
			DownloadURL:  cfg.Tracker.Rutor.DownloadURL,
			HourLimit:    cfg.Tracker.Rutor.HourLimit,
			Enabled:      cfg.Tracker.Rutor.Enabled,
		},
		},
		TeamHD: trackerModel.Tracker{
			Name:         cfg.Tracker.TeamHD.Name,
			Domain:       cfg.Tracker.TeamHD.Domain,
			ActiveDomain: cfg.Tracker.TeamHD.ActiveDomain,
			Mirrors:      cfg.Tracker.TeamHD.Mirrors,
			Publicity:    cfg.Tracker.TeamHD.Publicity,
			DownloadURL:  cfg.Tracker.TeamHD.DownloadURL,
			HourLimit:    cfg.Tracker.TeamHD.HourLimit,
			Cookies:      helper.ParseCookies(cfg.Tracker.TeamHD.Cookies),
			Enabled:      cfg.Tracker.TeamHD.Enabled,
		},
		NNM: trackerModel.Tracker{
			Name:         cfg.Tracker.NNM.Name,
			Domain:       cfg.Tracker.NNM.Domain,
			ActiveDomain: cfg.Tracker.NNM.ActiveDomain,
			Mirrors:      cfg.Tracker.NNM.Mirrors,
			Credentials:  cfg.Tracker.NNM.Credentials,
			ProxyType:    proxyModel.ProxyType(cfg.Tracker.NNM.ProxyType),
			Publicity:    cfg.Tracker.NNM.Publicity,
			LoginURL:     cfg.Tracker.NNM.LoginURL,
			DownloadURL:  cfg.Tracker.NNM.DownloadURL,
			HourLimit:    cfg.Tracker.NNM.HourLimit,
			Cookies:      helper.ParseCookies(cfg.Tracker.NNM.Cookies),
			Enabled:      cfg.Tracker.NNM.Enabled,
		},
	}
}
