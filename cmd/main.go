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
	"trackerr/internal/database"
	"trackerr/internal/helper"
	"trackerr/internal/manager"
	"trackerr/internal/manager/client/qbittorrent"
	"trackerr/internal/model"
	proxyModel "trackerr/internal/model/proxy"
	"trackerr/internal/proxy"
	"trackerr/internal/runner"
	"trackerr/internal/tracker"
)

type configuration struct {
	Runner       *config.Runner
	Manager      *config.Manager
	Flaresolverr *config.Flaresolverr
	DB           *config.Database
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
		db     database.IClient
		err    error
	)
	ctx := context.Background()

	if err = envconfig.Process(ctx, &cfg); err != nil {
		fmt.Println(err)
		return
	}

	// Init manager client
	// TODO: move creation to manager constructor
	if qbc, err = qbittorrent.New(
		model.ManagerConfig{
			Host:             cfg.Manager.QbitTorrent.Host,
			Username:         cfg.Manager.QbitTorrent.Username,
			Password:         cfg.Manager.QbitTorrent.Password,
			DeleteEnabled:    cfg.Manager.DeleteFilesEnabled,
			DeleteCategories: cfg.Manager.DeleteFilesCategories,
		}); err != nil {
		fmt.Println(err)
		return
	}

	// Init http client
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

	// Init proxy client
	px = proxy.New(ht, &proxyModel.Config{
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
		fmt.Println(err)
		return
	}
	defer dbConn.Close()
	// TODO: catch db conn errors
	db = database.New(dbConn)

	// Init trackers client
	tr := tracker.New(ht, px, db, &helper.CircuitBreakerConfig{
		MaxFailures: cfg.Runner.ConnMaxRetries,
		Timeout:     time.Duration(cfg.Runner.ConnMinutesToRetry) * time.Minute,
	})
	if err = tr.CacheAll(ctx); err != nil {
		fmt.Println(err)
		return
	}

	// Init handler client
	rn := runner.NewRunner(tr, manager.NewManager(qbc), db, model.HandlerConfig{
		ChunkSize:    cfg.Runner.ProcessSizeLimit,
		WatchTag:     cfg.Runner.WatchTag,
		ProcessDelay: time.Duration(cfg.Runner.ProcessDelay) * time.Minute,
		MinDateDiff:  cfg.Runner.UpdateThreshold * 60,
	})
	rn.Run(ctx)

	// Graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)
	defer func(sig os.Signal) {
		fmt.Println("goodbye")
	}(<-c)
}
