package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/joho/godotenv"
	"github.com/sethvargo/go-envconfig"

	"trackerr/internal/app"
	"trackerr/internal/config"
	"trackerr/internal/helper/breakeralerts"
	"trackerr/internal/model"
	"trackerr/internal/notify"
	"trackerr/internal/runner"
	"trackerr/internal/settings"
	"trackerr/internal/storage"
	"trackerr/internal/system/logger"
	webpkg "trackerr/internal/web"
)

func main() {
	loadEnv()

	ctx, stop := signalNotify()
	defer stop()

	baseCfg := config.DefaultApp()
	if err := envconfig.Process(ctx, &baseCfg); err != nil {
		logger.Error(err.Error())
		return
	}
	if err := baseCfg.Runner.Validate(); err != nil {
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
	defer func() { _ = dbConn.Close() }()

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
		cfg := baseCfg // структура копируется по значению, срезы в секциях overlay всегда переприсваивает
		if err := settings.ApplyOverrides(context.Background(), &cfg, db); err != nil {
			logger.Error(err.Error())
		}
		return cfg
	}

	// реестр переживает перезагрузку, поэтому хук вешается здесь один раз; каждый
	// вызов берёт свежий конфиг через effective(), чтобы подхватить настройки,
	// сохранённые уже после старта
	breakerAlerts.SetHook(func(open bool, tracker, errMsg string) {
		n := notify.New(notify.Config(effective().Notify))
		if open {
			n.Notify(notify.TrackerDownEvent(tracker, errMsg))
		} else {
			n.Notify(notify.TrackerUpEvent(tracker))
		}
	})

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
		cfg := baseCfg // структура копируется по значению, срезы в секциях overlay всегда переприсваивает
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
			WatchTags:            cfg.Runner.WatchTags,
			ThresholdMonth:       cfg.Runner.MonthThresholdDays,
			ThresholdHalfYear:    cfg.Runner.HalfYearThresholdDays,
			ThresholdYear:        cfg.Runner.YearThresholdDays,
			ThresholdOld:         cfg.Runner.YearThresholdDays,
			ListRefreshPerDay:    cfg.Runner.ListRefreshPerDay,
			StartHour:            cfg.Runner.StartHour,
			Categories:           cfg.Runner.AllowedCategories,
			Location:             scheduleLoc,
			Immediate:            first,
			ErrorStreakThreshold: cfg.Runner.ErrorStreakThreshold,
			MagnetTimeout:        cfg.Runner.MagnetTimeout,
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

func connectDB(ctx context.Context, cfg config.App) (*sql.DB, *storage.Service, error) {
	path := cfg.DBPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	dsn := path + "?_pragma=journal_mode(DELETE)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)"
	dbConn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, nil, err
	}
	// единственное пишущее соединение полностью снимает ошибки "database is locked".
	// журнал отката используется вместо WAL, потому что WAL нужен файл индекса в
	// разделяемой памяти, а bind mount в macOS не держит его согласованным, и процесс
	// на хосте, открывший тот же файл, может потерять закоммиченные транзакции
	dbConn.SetMaxOpenConns(1)
	if err := dbConn.PingContext(ctx); err != nil {
		_ = dbConn.Close()
		return nil, nil, err
	}
	return dbConn, storage.New(dbConn), nil
}

func signalNotify() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}

func loadEnv() {
	dir := os.Getenv("CONFIG_DIR")
	if dir == "" {
		dir = config.DefaultConfigDir
	}
	// сам CONFIG_DIR на этом этапе ещё не разобран через envconfig (курица и яйцо:
	// его нужно знать до загрузки .env, который может выставить остальные переменные),
	// поэтому читаем его прямо из окружения ОС.
	if envPath := filepath.Join(dir, ".env"); fileExists(envPath) {
		_ = godotenv.Load(envPath)
		return
	}
	_ = godotenv.Load()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
