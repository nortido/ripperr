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
	"ripperr/internal/mirrorsync"
	"ripperr/internal/model"
	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/notify"
	"ripperr/internal/proxy"
	"ripperr/internal/runner"
	"ripperr/internal/settings"
	"ripperr/internal/storage"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker"
	"ripperr/internal/tracker/registry"
	"ripperr/internal/webpush"
)

func Run(ctx context.Context, cfg config.App, db *storage.Service, handlerCfg model.HandlerConfig, runnerState *runner.State, alerts *breakeralerts.Registry, reload func()) error {
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
	// хост и учетные данные берем у активного клиента; имя нормализуем, иначе
	// MANAGER_CLIENT=Transmission (регистр, пробелы из env) уйдет в ветку qbit и упадет с ErrUnknownClient
	clientName := manager.NormalizeClient(cfg.Manager.Client)
	switch clientName {
	case manager.ClientTransmission:
		mcfg.Host = cfg.Manager.Transmission.Host
		mcfg.Username = cfg.Manager.Transmission.Username
		mcfg.Password = cfg.Manager.Transmission.Password
	case manager.ClientDeluge:
		mcfg.Host = cfg.Manager.Deluge.Host
		mcfg.Password = cfg.Manager.Deluge.Password
	case manager.ClientDownloadStation:
		mcfg.Host = cfg.Manager.DownloadStation.Host
		mcfg.Username = cfg.Manager.DownloadStation.Username
		mcfg.Password = cfg.Manager.DownloadStation.Password
	default:
		mcfg.Host = cfg.Manager.QbitTorrent.Host
		mcfg.Username = cfg.Manager.QbitTorrent.Username
		mcfg.Password = cfg.Manager.QbitTorrent.Password
		if clientName == "" {
			clientName = manager.ClientQbit
		}
	}
	// у Deluge и Download Station нет тегов: режим тегов дал бы пустую выборку, поэтому смотрим по категориям
	supportsTags := manager.SupportsTags(clientName)
	if cfg.Runner.EffectiveWatchMode() == config.WatchModeTags && !supportsTags {
		logger.Info(fmt.Sprintf("%s has no tags, watching by categories %v", clientName, cfg.Runner.AllowedCategories))
	}
	handlerCfg.Categories, handlerCfg.WatchTags = cfg.Runner.WatchSelection(supportsTags)
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
			URL:         cfg.Flaresolverr.Host,
			MaxTimeout:  cfg.Flaresolverr.MaxTimeout,
			CaptureHost: cfg.Flaresolverr.CaptureHost,
			CapturePort: cfg.Flaresolverr.CapturePort,
		},
	})

	tr := tracker.New(ht, downloadHt, px, db, &helper.CircuitBreakerConfig{
		MaxFailures: cfg.Runner.CBRetries,
		Timeout:     time.Duration(cfg.Runner.CBMinutesToRetry) * time.Minute,
	}, tracker.ConfigFromApp(cfg), alerts)
	if alerts != nil {
		names := make([]string, 0, len(tr.CachedList()))
		for _, trk := range tr.CachedList() {
			names = append(names, trk.GetName())
		}
		alerts.Retain(names)
	}
	nt := notify.New(notify.Config(cfg.Notify), cfg.Runner.DownloadProxy).WithAppURL(cfg.Web.AppURL).WithPush(webpush.NewStore(db)).WithEvents(db).WithTelegramMutes(notify.NewTelegramMutes(db))
	tgCtx, tgStop := context.WithCancel(ctx)
	tgDone := make(chan struct{})
	go func() {
		nt.RunTelegramButtons(tgCtx)
		close(tgDone)
	}()
	defer func() {
		tgStop()
		<-tgDone
	}()
	tr.SetMirrorSwitchHook(func(name, from, to string) {
		nt.Notify(notify.MirrorSwitchedEvent(name, from, to))
	})
	tr.SetMirrorPauseHook(func(ttid int, host string) {
		key := registry.Key(ttid)
		if key == "" {
			return
		}
		// хук зовется под блокировкой кэша трекера: только короткая запись в БД, без сети
		changed := false
		err := settings.UpdateMirrorSync(ctx, db, func(d *settings.MirrorSync) {
			changed = false
			prev, ok := d.Paused[key]
			switch {
			case host == "":
				delete(d.Paused, key)
			case !ok || settings.MirrorHostKey(prev.Host) != settings.MirrorHostKey(host):
				if d.Paused == nil {
					d.Paused = map[string]settings.MirrorPause{}
				}
				d.Paused[key] = settings.MirrorPause{Host: host, Since: time.Now()}
				changed = true
			}
		})
		if err != nil {
			logger.Warn(fmt.Sprintf("mirror sync: save failed: %v", err))
			return
		}
		if !changed {
			return
		}
		name := key
		if item := cfg.Tracker[key]; item != nil && item.Name != "" {
			name = item.Name
		}
		// Notify только запускает горутины отправки, сети под блокировкой нет
		nt.Notify(notify.MirrorPausedEvent(name, host))
	})
	defer func() {
		// ctx к выходу уже отменен, а FlareSolverr не должен держать остановку дольше 3 с
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		tr.DestroyFlareSessions(cctx)
	}()
	mgr := manager.NewManager(mc)
	msDeps := mirrorsync.Deps{
		Store:     db,
		Items:     cfg.Tracker,
		HTTP:      mirrorsync.Client(cfg.MirrorSource, cfg.Runner.DownloadProxy, 30*time.Second),
		Sources:   mirrorsync.SourceURLs(cfg.MirrorSource),
		Check:     tracker.HostChecker(ht),
		ConfirmOn: cfg.Runner.MirrorConfirm,
		Notify:    nt.Notify,
		Reload:    reload,
		Idle:      func() bool { return !runnerState.Running() },
		Entries: func(ctx context.Context) ([]string, error) {
			list, err := mgr.GetEntries(ctx, model.FilterOptions{})
			urls := make([]string, 0, len(list))
			for _, e := range list {
				if e != nil && e.URL != "" {
					urls = append(urls, e.URL)
				}
			}
			return urls, err
		},
		StoredURLs: func(ctx context.Context) ([]string, error) {
			items, err := db.ListItems(ctx)
			if err != nil {
				return nil, err
			}
			urls := make([]string, 0, len(items))
			for _, it := range items {
				urls = append(urls, it.URL)
			}
			return urls, nil
		},
	}
	if err = tr.BuildCache(ctx); err != nil {
		// без живого трекера приложение уходит в повтор и фоновая проверка не успеет, а новое зеркало нужнее всего именно тогда
		mirrorsync.RunOnceIfDue(ctx, msDeps)
		return err
	}

	rn := runner.NewRunner(tr, mgr, db, handlerCfg, runnerState, nt)

	bhDone := make(chan struct{})
	go func() {
		mc.SweepBlackhole(ctx)
		close(bhDone)
	}()

	msDone := make(chan struct{})
	go func() {
		mirrorsync.Run(ctx, msDeps)
		close(msDone)
	}()

	done := make(chan struct{})
	go func() {
		rn.Run(ctx)
		close(done)
	}()
	<-done
	<-bhDone
	<-msDone
	return ctx.Err()
}
