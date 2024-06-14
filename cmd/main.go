package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/sethvargo/go-envconfig"

	"torr/internal/config"
	"torr/internal/handler"
	"torr/internal/manager"
	"torr/internal/manager/client/qbittorrent"
	"torr/internal/model"
	"torr/internal/storage"
	"torr/internal/tracker"
	"torr/internal/tracker/proxy/flaresolverr"
)

type configuration struct {
	Manager      *config.Manager
	Flaresolverr *config.Flaresolverr
}

func main() {
	var (
		tc  qbittorrent.Client
		fs  flaresolverr.Client
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
			Host:     cfg.Manager.QbitTorrent.Host,
			Username: cfg.Manager.QbitTorrent.Username,
			Password: cfg.Manager.QbitTorrent.Password,
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
	s := tracker.New(ht, fs)
	if err = s.CacheAll(ctx); err != nil {
		fmt.Println(err)
	}
	h := handler.NewHandler(s, manager.NewManager(tc), storage.NewStorage())
	h.Handle(ctx)
}
