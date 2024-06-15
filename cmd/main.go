package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sethvargo/go-envconfig"

	"torr/internal/config"
	"torr/internal/manager"
	"torr/internal/manager/client/qbittorrent"
	"torr/internal/model"
	"torr/internal/runner"
	"torr/internal/tracker"
	"torr/internal/tracker/proxy/flaresolverr"
)

type configuration struct {
	Runner       *config.Runner
	Manager      *config.Manager
	Flaresolverr *config.Flaresolverr
}

func main() {
	var (
		tc  qbittorrent.IClient
		fs  flaresolverr.IClient
		ht  *http.Client
		err error
		cfg configuration
	)
	ctx := context.Background()

	if err = envconfig.Process(ctx, &cfg); err != nil {
		fmt.Println(err)
		return
	}
	tc, err = qbittorrent.New(
		model.ManagerConfig{
			Host:        cfg.Manager.QbitTorrent.Host,
			Username:    cfg.Manager.QbitTorrent.Username,
			Password:    cfg.Manager.QbitTorrent.Password,
			DeleteFiles: cfg.Manager.AllowDeleteFiles,
		})
	if err != nil {
		fmt.Println(err)
		return
	}
	ht = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	fs = flaresolverr.New(ht, &flaresolverr.Config{
		Host:       cfg.Flaresolverr.Host,
		MaxTimeout: cfg.Flaresolverr.MaxTimeout,
	})
	// TODO: Add global proxy switcher
	s := tracker.New(ht, fs)
	// TODO: Add repeatable re-cache
	if err = s.CacheAll(ctx); err != nil {
		fmt.Println(err)
	}
	// TODO: Add global proxy switcher
	h := runner.NewRunner(s, manager.NewManager(tc), model.HandlerConfig{
		RunsPerDay: cfg.Runner.RunsPerDay,
		WatchTag:   cfg.Runner.WatchTag,
		ProcessTimeout: func() time.Duration {
			return time.Duration(cfg.Runner.MaxTimeout) * time.Second
		}(),
		MinDateDiff: cfg.Runner.MinTimeDiff,
	})
	h.Run(ctx)
}
