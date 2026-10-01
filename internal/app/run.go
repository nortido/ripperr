package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/helper"
	"ripperr/internal/helper/breakeralerts"
	"ripperr/internal/helper/tlsclient"
	"ripperr/internal/manager"
	"ripperr/internal/model"
	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/notify"
	"ripperr/internal/proxy"
	"ripperr/internal/runner"
	"ripperr/internal/storage"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker"
)

func Run(ctx context.Context, cfg config.App, db *storage.Service, handlerCfg model.HandlerConfig, runnerState *runner.State, alerts *breakeralerts.Registry) error {
	var (
		mc         manager.Client
		px         proxy.IClient
		ht         *http.Client
		downloadHt helper.HTTPDoer
		err        error
	)

	mcfg := model.ManagerConfig{
		PausedMaxRatio:     cfg.Manager.PausedMaxRatio,
		DeleteEnabled:      cfg.Manager.DeleteEnabled,
		DeleteFilesEnabled: cfg.Manager.DeleteFilesEnabled,
		DeleteCategories:   cfg.Manager.DeleteFilesCategories,
		BlackholeDir:       cfg.BlackholeDirPath(),
	}
	// поля хоста/логина/пароля берутся у активного клиента, остальные общие для обоих;
	// нормализация в одном месте, иначе MANAGER_CLIENT=Transmission (регистр,
	// пробелы из env) уходит в ветку qbit за учётными данными и потом падает с
	// ErrUnknownClient
	clientName := manager.NormalizeClient(cfg.Manager.Client)
	if clientName == manager.ClientTransmission {
		mcfg.Host = cfg.Manager.Transmission.Host
		mcfg.Username = cfg.Manager.Transmission.Username
		mcfg.Password = cfg.Manager.Transmission.Password
	} else {
		mcfg.Host = cfg.Manager.QbitTorrent.Host
		mcfg.Username = cfg.Manager.QbitTorrent.Username
		mcfg.Password = cfg.Manager.QbitTorrent.Password
		if clientName == "" {
			clientName = manager.ClientQbit
		}
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if mc, err = manager.NewClient(clientName, mcfg); err != nil {
			// неизвестный вид клиента никогда не подключится, повторять бессмысленно
			if errors.Is(err, manager.ErrUnknownClient) {
				return err
			}
			err = fmt.Errorf("failed to connect to %s: %w", clientName, err)
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
		Transport:     helper.ProxyTransport(cfg.Runner.DownloadProxy),
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
		Timeout:   time.Duration(cfg.Flaresolverr.MaxTimeout)*time.Millisecond + 30*time.Second,
		Transport: helper.DirectTransport(),
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

	rn := runner.NewRunner(tr, manager.NewManager(mc), db, handlerCfg, runnerState, notify.New(notify.Config(cfg.Notify), cfg.Runner.DownloadProxy))

	bhDone := make(chan struct{})
	go func() {
		mc.SweepBlackhole(ctx)
		close(bhDone)
	}()

	done := make(chan struct{})
	go func() {
		rn.Run(ctx)
		close(done)
	}()
	<-done
	<-bhDone
	return ctx.Err()
}
