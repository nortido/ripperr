package main

import (
	"context"
	"errors"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/sethvargo/go-envconfig"

	"trackerr/internal/app"
	"trackerr/internal/config"
	"trackerr/internal/helper/breakeralerts"
	"trackerr/internal/model"
	"trackerr/internal/runner"
	"trackerr/internal/settings"
	"trackerr/internal/storage"
	"trackerr/internal/system/logger"
	webpkg "trackerr/internal/web"
)

func main() {
	_ = godotenv.Load()

	ctx, stop := signalNotify()
	defer stop()

	var baseCfg config.App
	if err := envconfig.Process(ctx, &baseCfg); err != nil {
		logger.Error(err.Error())
		return
	}
	baseCfg.Runner.WatchTags = settings.WatchTagsFromEnv(baseCfg.Runner.WatchTag)
	logger.SetLevel(baseCfg.Log.Level)

	dbConn, db, err := connectDB(ctx, baseCfg)
	if err != nil {
		logger.Error(err.Error())
		return
	}
	defer dbConn.Close()

	if err = db.Bootstrap(ctx); err != nil {
		logger.Error(err.Error())
		return
	}

	reloadCh := make(chan struct{}, 1)
	runnerState := runner.NewState()
	breakerAlerts := breakeralerts.New()

	reload := func() {
		select {
		case reloadCh <- struct{}{}:
		default:
		}
	}

	effective := func() config.App {
		cfg := baseCfg.Clone()
		if err := settings.ApplyOverrides(context.Background(), &cfg, db); err != nil {
			logger.Error(err.Error())
		}
		return cfg
	}

	var webSrv *webpkg.Server
	if baseCfg.Web.Enabled {
		webSrv = webpkg.New(db, reload, effective, baseCfg.Web.Listen, runnerState, breakerAlerts)
		go func() {
			if err := webSrv.Start(); err != nil && ctx.Err() == nil {
				logger.Error(err.Error())
			}
		}()
		defer func() {
			shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = webSrv.Shutdown(shCtx)
		}()
	}

	first := true
	for {
		cfg := baseCfg.Clone()
		if err := settings.ApplyOverrides(ctx, &cfg, db); err != nil {
			logger.Error(err.Error())
		}

		appCtx, cancel := context.WithCancel(ctx)

		var scheduleLoc *time.Location
		if cfg.Runner.Timezone != "" {
			if l, err := time.LoadLocation(cfg.Runner.Timezone); err != nil {
				logger.Error("invalid timezone " + cfg.Runner.Timezone + ": " + err.Error())
			} else {
				scheduleLoc = l
			}
		}

		handlerCfg := model.HandlerConfig{
			WatchTags:         cfg.Runner.WatchTags,
			ThresholdMonth:    cfg.Runner.MonthThresholdDays,
			ThresholdHalfYear: cfg.Runner.HalfYearThresholdDays,
			ThresholdYear:     cfg.Runner.YearThresholdDays,
			ThresholdOld:      cfg.Runner.YearThresholdDays,
			ListRefreshPerDay: cfg.Runner.ListRefreshPerDay,
			StartHour:         cfg.Runner.StartHour,
			Categories:        cfg.Runner.AllowedCategories,
			Location:          scheduleLoc,
			Immediate:         first,
		}

		done := make(chan struct{})
		var runErr error
		go func() {
			runErr = app.Run(appCtx, cfg, db, handlerCfg, runnerState, breakerAlerts)
			close(done)
		}()

		select {
		case <-ctx.Done():
			cancel()
			<-done
			first = false
			logger.Info("goodbye")
			return
		case <-reloadCh:
			cancel()
			<-done
			first = false
		case <-done:
			if ctx.Err() != nil {
				logger.Info("goodbye")
				return
			}
			if runErr == nil || errors.Is(runErr, context.Canceled) {
				first = false
			}
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				logger.Error(runErr.Error())
			}
			backoff := time.NewTimer(time.Duration(cfg.Manager.MinutesToReconnect) * time.Minute)
			select {
			case <-ctx.Done():
				backoff.Stop()
				logger.Info("goodbye")
				return
			case <-reloadCh:
				backoff.Stop()
			case <-backoff.C:
			}
		}
	}
}

func connectDB(ctx context.Context, cfg config.App) (*pgxpool.Pool, storage.IClient, error) {
	dbCfg, err := pgxpool.ParseConfig(cfg.DB.MasterURL)
	if err != nil {
		return nil, nil, err
	}
	dbCfg.ConnConfig.ConnectTimeout = time.Duration(cfg.DB.ConnectTimeout) * time.Second
	dbConn, err := pgxpool.NewWithConfig(ctx, dbCfg)
	if err != nil {
		return nil, nil, err
	}
	return dbConn, storage.New(dbConn), nil
}

func signalNotify() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}
