package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"time"

	"trackerr/internal/config"
	"trackerr/internal/helper"
	"trackerr/internal/helper/breakeralerts"
	"trackerr/internal/helper/tlsclient"
	"trackerr/internal/manager"
	"trackerr/internal/manager/client/qbittorrent"
	"trackerr/internal/model"
	proxyModel "trackerr/internal/model/proxy"
	"trackerr/internal/proxy"
	"trackerr/internal/runner"
	"trackerr/internal/storage"
	"trackerr/internal/system/logger"
	"trackerr/internal/tracker"
)

// Run builds dependencies and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg config.App, db storage.IClient, handlerCfg model.HandlerConfig, runnerState *runner.State, alerts *breakeralerts.Registry) error {
	var (
		qbc        qbittorrent.IClient
		px         proxy.IClient
		ht         *http.Client
		downloadHt helper.HTTPDoer
		err        error
	)

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
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if qbc, err = qbittorrent.New(qbtcfg); err != nil {
			err = fmt.Errorf("failed to connect to qbittorrent: %w", err)
			logger.Error(err.Error())
			t := time.NewTimer(time.Duration(cfg.Manager.MinutesToReconnect) * time.Minute)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
			continue
		}
		break
	}

	var cj *cookiejar.Jar
	if cj, err = cookiejar.New(nil); err != nil {
		return err
	}
	redirectPolicy := func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	ht = &http.Client{
		CheckRedirect: redirectPolicy,
		Jar:           cj,
		Timeout:       time.Duration(cfg.Runner.HttpTimeout) * time.Second,
	}
	downloadHt, err = tlsclient.New(tlsclient.Config{
		Timeout:  time.Duration(cfg.Runner.DownloadTimeout) * time.Second,
		ProxyURL: cfg.Runner.DownloadProxy,
		Profile:  cfg.Runner.TLSProfile,
	})
	if err != nil {
		return err
	}

	px = proxy.New(&http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: time.Duration(cfg.Flaresolverr.MaxTimeout)*time.Millisecond + 30*time.Second,
	}, &proxyModel.Config{
		Flaresolverr: proxyModel.ItemConfig{
			URL:        cfg.Flaresolverr.Host,
			MaxTimeout: cfg.Flaresolverr.MaxTimeout,
		},
	})

	tr := tracker.New(ht, downloadHt, px, db, &helper.CircuitBreakerConfig{
		MaxFailures: cfg.Runner.CBRetries,
		Timeout:     time.Duration(cfg.Runner.CBMinutesToRetry) * time.Minute,
	}, tracker.ConfigFromApp(cfg), alerts)
	defer func() {
		tr.DestroyFlareSessions()
		if alerts != nil {
			for _, trk := range tr.CachedList() {
				alerts.Close(trk.GetName())
			}
		}
	}()
	if err = tr.BuildCache(ctx); err != nil {
		return err
	}

	rn := runner.NewRunner(tr, manager.NewManager(qbc), db, handlerCfg, runnerState)
	done := make(chan struct{})
	go func() {
		rn.Run(ctx)
		close(done)
	}()
	<-done
	return ctx.Err()
}
