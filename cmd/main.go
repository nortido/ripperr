package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sethvargo/go-envconfig"

	"trackerr/internal/config"
	"trackerr/internal/helper"
	"trackerr/internal/manager"
	"trackerr/internal/manager/client/qbittorrent"
	"trackerr/internal/model"
	proxyModel "trackerr/internal/model/proxy"
	trackerModel "trackerr/internal/model/tracker"
	"trackerr/internal/proxy"
	"trackerr/internal/runner"
	"trackerr/internal/storage"
	"trackerr/internal/system/logger"
	"trackerr/internal/tracker"
)

type configuration struct {
	Runner       *config.Runner
	Tracker      *config.Tracker
	Manager      *config.Manager
	Flaresolverr *config.Flaresolverr
	DB           *config.Storage
}

func main() {
	var (
		cfg    configuration
		qbc    qbittorrent.IClient
		px     proxy.IClient
		ht     *http.Client
		cj     *cookiejar.Jar
		dbConn *pgxpool.Pool
		dbCfg  *pgxpool.Config
		db     storage.IClient
		err    error
	)
	// TODO: gracefull shutdown
	ctx := context.Background()

	if err = envconfig.Process(ctx, &cfg); err != nil {
		logger.Error(err.Error())
		return
	}

	// Init manager client
	// TODO: move creation to manager constructor
	qbtcfg := model.ManagerConfig{
		Host:               cfg.Manager.QbitTorrent.Host,
		Username:           cfg.Manager.QbitTorrent.Username,
		Password:           cfg.Manager.QbitTorrent.Password,
		PausedMaxRatio:     cfg.Manager.PausedMaxRatio,
		DeleteEnabled:      cfg.Manager.DeleteEnabled,
		DeleteFilesEnabled: cfg.Manager.DeleteFilesEnabled,
		DeleteCategories:   cfg.Manager.DeleteFilesCategories,
	}
	for {
		if qbc, err = qbittorrent.New(qbtcfg); err != nil {
			err = fmt.Errorf("failed to connect to qbittorrent: %w", err)
			logger.Error(err.Error())
			t := time.NewTimer(time.Duration(cfg.Manager.MinutesToReconnect) * time.Minute)
			<-t.C
			continue
		}
		break
	}
	// Init http client
	if cj, err = cookiejar.New(nil); err != nil {
		logger.Error(err.Error())
		return
	}
	ht = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Jar:     cj,
		Timeout: time.Duration(cfg.Runner.HttpTimeout) * time.Second,
	}

	// Init proxy client
	px = proxy.New(&http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: time.Duration(cfg.Flaresolverr.MaxTimeout) * time.Second,
	}, &proxyModel.Config{
		None: proxyModel.ItemConfig{},
		Flaresolverr: proxyModel.ItemConfig{
			URL:        cfg.Flaresolverr.Host,
			MaxTimeout: cfg.Flaresolverr.MaxTimeout,
		},
		Http: proxyModel.ItemConfig{},
	})

	// Init db client
	dbCfg, _ = pgxpool.ParseConfig(cfg.DB.MasterURL)
	dbCfg.ConnConfig.ConnectTimeout = time.Duration(cfg.DB.ConnectTimeout) * time.Second
	if dbConn, err = pgxpool.NewWithConfig(ctx, dbCfg); err != nil {
		logger.Error(err.Error())
		return
	}
	defer dbConn.Close()
	// TODO: catch db conn errors
	db = storage.New(dbConn)

	// Init trackers client
	tr := tracker.New(ht, px, db, &helper.CircuitBreakerConfig{
		MaxFailures: cfg.Runner.CBRetries,
		Timeout:     time.Duration(cfg.Runner.CBMinutesToRetry) * time.Minute,
	}, &trackerModel.TrackerConfig{
		Rutracker: trackerModel.Tracker{
			Name:        cfg.Tracker.Rutracker.Name,
			Domain:      cfg.Tracker.Rutracker.Domain,
			Credentials: cfg.Tracker.Rutracker.Credentials,
			ProxyType:   proxyModel.ProxyType(cfg.Tracker.Rutracker.ProxyType),
			Publicity:   cfg.Tracker.Rutracker.Publicity,
			LoginURL:    cfg.Tracker.Rutracker.LoginURL,
			DownloadURL: cfg.Tracker.Rutracker.DownloadURL,
			HourLimit:   cfg.Tracker.Rutracker.HourLimit,
			Enabled:     cfg.Tracker.Rutracker.Enabled,
		},
		Kinozal: trackerModel.Tracker{
			Name:        cfg.Tracker.Kinozal.Name,
			Domain:      cfg.Tracker.Kinozal.Domain,
			Credentials: cfg.Tracker.Kinozal.Credentials,
			ProxyType:   proxyModel.ProxyType(cfg.Tracker.Kinozal.ProxyType),
			Publicity:   cfg.Tracker.Kinozal.Publicity,
			LoginURL:    cfg.Tracker.Kinozal.LoginURL,
			DownloadURL: cfg.Tracker.Kinozal.DownloadURL,
			HourLimit:   cfg.Tracker.Kinozal.HourLimit,
			Cookies:     helper.ParseCookies(cfg.Tracker.Kinozal.Cookies, ""),
			Enabled:     cfg.Tracker.Kinozal.Enabled,
		},
		Rutor: trackerModel.Tracker{
			Name:        cfg.Tracker.Rutor.Name,
			Domain:      cfg.Tracker.Rutor.Domain,
			Publicity:   cfg.Tracker.Rutor.Publicity,
			DownloadURL: cfg.Tracker.Rutor.DownloadURL,
			HourLimit:   cfg.Tracker.Rutor.HourLimit,
			Enabled:     cfg.Tracker.Rutor.Enabled,
		},
		},
		TeamHD: trackerModel.Tracker{
			Name:        cfg.Tracker.TeamHD.Name,
			Domain:      cfg.Tracker.TeamHD.Domain,
			Publicity:   cfg.Tracker.TeamHD.Publicity,
			DownloadURL: cfg.Tracker.TeamHD.DownloadURL,
			HourLimit:   cfg.Tracker.TeamHD.HourLimit,
			Cookies:     helper.ParseCookies(cfg.Tracker.TeamHD.Cookies, ""),
			Enabled:     cfg.Tracker.TeamHD.Enabled,
		},
		NNM: trackerModel.Tracker{
			Name:        cfg.Tracker.NNM.Name,
			Domain:      cfg.Tracker.NNM.Domain,
			Credentials: cfg.Tracker.NNM.Credentials,
			ProxyType:   proxyModel.ProxyType(cfg.Tracker.NNM.ProxyType),
			Publicity:   cfg.Tracker.NNM.Publicity,
			LoginURL:    cfg.Tracker.NNM.LoginURL,
			DownloadURL: cfg.Tracker.NNM.DownloadURL,
			HourLimit:   cfg.Tracker.NNM.HourLimit,
			Cookies:     helper.ParseCookies(cfg.Tracker.NNM.Cookies, ""),
			Enabled:     cfg.Tracker.NNM.Enabled,
		},
	})
	if err = tr.BuildCache(ctx); err != nil {
		logger.Error(err.Error())
		return
	}

	// Init handler client
	rn := runner.NewRunner(tr, manager.NewManager(qbc), db, model.HandlerConfig{
		WatchTag:          cfg.Runner.WatchTag,
		WorkerDelay:       cfg.Runner.WorkerDelay,
		ThresholdMonth:    cfg.Runner.MonthThresholdDays,
		ThresholdHalfYear: cfg.Runner.HalfYearThresholdDays,
		ThresholdYear:     cfg.Runner.YearThresholdDays,
		ThresholdOld:      cfg.Runner.YearThresholdDays,
		ListRefreshPerDay: cfg.Runner.ListRefreshPerDay,
		Categories:        cfg.Runner.AllowedCategories,
	})
	rn.Run(ctx)

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
	defer func(sig os.Signal) {
		logger.Info("goodbye")
	}(<-c)
}
