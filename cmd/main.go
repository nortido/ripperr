package main

import (
	"context"
	"fmt"

	"github.com/sethvargo/go-envconfig"

	"torr/internal/config"
	"torr/internal/handler"
	"torr/internal/manager"
	"torr/internal/manager/client/qbittorrent"
	"torr/internal/model"
	"torr/internal/storage"
	"torr/internal/tracker"
	"torr/internal/tracker/router"
)

type configuration struct {
	Manager *config.Manager
}

func main() {
	var (
		tc  qbittorrent.Client
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
	rtr := router.New()
	if err = rtr.CacheTrackers(ctx); err != nil {
		fmt.Println(err)
	}
	h := handler.NewHandler(tracker.New(rtr), manager.NewManager(tc), storage.NewStorage())
	h.Handle(ctx)
}
