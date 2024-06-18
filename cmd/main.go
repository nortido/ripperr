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

	"github.com/sethvargo/go-envconfig"

	"torr/internal/config"
	"torr/internal/helper"
	"torr/internal/manager"
	"torr/internal/manager/client/qbittorrent"
	"torr/internal/model"
	mproxy "torr/internal/model/proxy"
	"torr/internal/proxy"
	"torr/internal/runner"
	"torr/internal/tracker"
)

type configuration struct {
	Runner       *config.Runner
	Manager      *config.Manager
	Flaresolverr *config.Flaresolverr
}

func main() {
	var (
		qbc qbittorrent.IClient
		px  proxy.IClient
		ht  *http.Client
		cj  *cookiejar.Jar
		err error
		cfg configuration
	)
	ctx := context.Background()

	if err = envconfig.Process(ctx, &cfg); err != nil {
		fmt.Println(err)
		return
	}
	qbc, err = qbittorrent.New(
		model.ManagerConfig{
			Host:             cfg.Manager.QbitTorrent.Host,
			Username:         cfg.Manager.QbitTorrent.Username,
			Password:         cfg.Manager.QbitTorrent.Password,
			DeleteEnabled:    cfg.Manager.DeleteFilesEnabled,
			DeleteCategories: cfg.Manager.DeleteFilesCategories,
		})
	if err != nil {
		fmt.Println(err)
		return
	}
	if cj, err = cookiejar.New(nil); err != nil {
		fmt.Println(err)
		return
	}
	ht = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Jar:     cj,
		Timeout: time.Duration(cfg.Runner.HttpTimeout) * time.Second,
	}
	px = proxy.New(ht, &mproxy.Config{
		None: mproxy.ItemConfig{},
		Flaresolverr: mproxy.ItemConfig{
			URL:        cfg.Flaresolverr.Host,
			MaxTimeout: cfg.Flaresolverr.MaxTimeout,
		},
		Http: mproxy.ItemConfig{},
	})
	s := tracker.New(ht, px, &helper.CircuitBreakerConfig{
		MaxFailures: cfg.Runner.ConnMaxRetries,
		Timeout:     time.Duration(cfg.Runner.ConnMinutesToRetry) * time.Minute,
	})
	if err = s.CacheAll(ctx); err != nil {
		fmt.Println(err)
	}
	h := runner.NewRunner(s, manager.NewManager(qbc), model.HandlerConfig{
		RunsPerDay:   cfg.Runner.RunsPerDay,
		WatchTag:     cfg.Runner.WatchTag,
		ProcessDelay: time.Duration(cfg.Runner.ProcessSecondsDelay) * time.Second,
		MinDateDiff:  cfg.Runner.UpdateThreshold,
	})
	h.Run(ctx)

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
	defer func(sig os.Signal) {
		fmt.Println("goodbye")
	}(<-c)
}
