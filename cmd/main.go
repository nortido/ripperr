package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/joho/godotenv"
	"github.com/sethvargo/go-envconfig"

	"ripperr/internal/app"
	"ripperr/internal/config"
	"ripperr/internal/helper/breakeralerts"
	"ripperr/internal/model"
	"ripperr/internal/notify"
	"ripperr/internal/runner"
	"ripperr/internal/settings"
	"ripperr/internal/storage"
	"ripperr/internal/storage/client/sqlite"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker"
	"ripperr/internal/tracker/registry"
	_ "ripperr/internal/tracker/registry/all"
	webpkg "ripperr/internal/web"
)

func main() {
	loadEnv()

	if len(os.Args) > 1 && os.Args[1] == "decrypt-secrets" {
		os.Exit(decryptSecrets())
	}

	// до envconfig, чтобы ошибки конфига при падении контейнера в цикле попали в файл
	logger.InitFile(filepath.Join(configDirFromEnv(), "logs"))

	ctx, stop := signalNotify()
	defer stop()

	baseCfg := config.DefaultApp()
	// дефолты трекеров объявляет сам пакет трекера. env накладываем отдельным проходом:
	// ключи известны только в рантайме
	baseCfg.Tracker = registry.ConfigDefaults()
	if err := envconfig.Process(ctx, &baseCfg); err != nil {
		logger.Error(err.Error())
		return
	}
	if err := config.ApplyTrackerEnv(ctx, baseCfg.Tracker, envconfig.OsLookuper()); err != nil {
		logger.Error(err.Error())
		return
	}
	if err := baseCfg.Runner.Validate(); err != nil {
		logger.Error(err.Error())
		return
	}
	baseCfg.Runner.WatchTags = settings.WatchTagsFromEnv(baseCfg.Runner.WatchTag)
	// адрес из БД проверен еще при сохранении, а из env приходит как есть.
	// Без схемы ProxyTransport молча пошел бы напрямую
	if err := settings.ValidateDownloadProxy(baseCfg.Runner.DownloadProxy); err != nil {
		logger.Error("RUNNER_DOWNLOAD_PROXY: " + err.Error())
		return
	}
	if err := settings.ValidateMirrorSource(baseCfg.MirrorSource); err != nil {
		logger.Error("MIRROR_SOURCE: " + err.Error())
		return
	}
	logger.SetLevel(baseCfg.Log.Level)

	dbConn, err := connectDB(ctx, baseCfg)
	if err != nil {
		logger.Error(err.Error())
		return
	}
	defer func() { _ = dbConn.Close() }()

	db, openInfo, err := storage.Open(ctx, dbConn, baseCfg.SecretsDir(), os.LookupEnv)
	if err != nil {
		logger.Error(err.Error())
		return
	}
	logger.Info("secrets key: " + openInfo.KeySource)
	if openInfo.KeyCheckResealed {
		logger.Info("secrets: key check re-sealed, older builds can no longer open this database")
	}
	if openInfo.Migrated {
		logger.Info("secrets: stored settings and tracker credentials encrypted")
	}
	if openInfo.Vacuumed {
		logger.Info("secrets: database compacted, old plaintext purged")
	}
	if openInfo.VacuumErr != nil {
		logger.Warn("secrets: vacuum pending, retry on next start: " + openInfo.VacuumErr.Error())
	}
	if err := tracker.MigrateSettings(ctx, db, baseCfg); err != nil {
		logger.Error("trackers: settings migration: " + err.Error())
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
		cfg := baseCfg // копии по значению хватает: overlay срезы в секциях всегда переприсваивает
		if err := settings.ApplyOverrides(context.Background(), &cfg, db); err != nil {
			logger.Error(err.Error())
		}
		return cfg
	}

	// реестр переживает перезагрузку, хук вешаем один раз. Конфиг берем через effective(),
	// чтобы видеть настройки, сохраненные после старта
	breakerAlerts.SetHook(func(open bool, tracker, errMsg string) {
		ecfg := effective()
		n := notify.New(notify.Config(ecfg.Notify), ecfg.Runner.DownloadProxy).WithAppURL(ecfg.Web.AppURL)
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
		cfg := baseCfg // см. выше, копии по значению хватает
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
			runErr = app.Run(appCtx, cfg, db, handlerCfg, runnerState, breakerAlerts, reload)
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

func decryptSecrets() int {
	ctx := context.Background()
	cfg := config.DefaultApp()
	if err := envconfig.Process(ctx, &cfg); err != nil {
		logger.Error(err.Error())
		return 1
	}
	logger.SetLevel(cfg.Log.Level)

	dbConn, err := connectDB(ctx, cfg)
	if err != nil {
		logger.Error("secrets: decrypt failed: " + err.Error())
		return 1
	}
	err = storage.DecryptSecrets(ctx, dbConn, cfg.SecretsDir(), os.LookupEnv)
	_ = dbConn.Close()
	switch {
	case errors.Is(err, sqlite.ErrNotEncrypted):
		logger.Info("secrets: database is not encrypted, nothing to do")
		return 0
	case err != nil:
		logger.Error("secrets: decrypt failed: " + strings.TrimPrefix(err.Error(), "secrets: "))
		return 1
	}
	logger.Info("secrets: decrypted, database is back at schema version " + strconv.Itoa(sqlite.SecretsVersion-1))
	return 0
}

func connectDB(ctx context.Context, cfg config.App) (*sql.DB, error) {
	path := cfg.DBPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// _txlock=immediate берет пишущую блокировку сразу на BEGIN. Соединение у нас одно, так что
	// внутри процесса это бесплатно, зато накат миграций не гоняется с чужим процессом
	// на том же файле
	dsn := path + "?_pragma=journal_mode(DELETE)&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)&_txlock=immediate"
	dbConn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// одно соединение, и "database is locked" больше не бывает. WAL не берем: ему нужен индекс
	// в разделяемой памяти, а bind mount в macOS его не синхронизирует, и процесс на хосте
	// может потерять закомиченные транзакции
	dbConn.SetMaxOpenConns(1)
	if err := dbConn.PingContext(ctx); err != nil {
		_ = dbConn.Close()
		return nil, err
	}
	return dbConn, nil
}

func signalNotify() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
}

func loadEnv() {
	// CONFIG_DIR тут еще не разобран envconfig: он нужен до загрузки .env, а .env может
	// задать остальное. Поэтому читаем его прямо из окружения
	if envPath := filepath.Join(configDirFromEnv(), ".env"); fileExists(envPath) {
		_ = godotenv.Load(envPath)
		return
	}
	_ = godotenv.Load()
}

func configDirFromEnv() string {
	if dir := os.Getenv("CONFIG_DIR"); dir != "" {
		return dir
	}
	return config.DefaultConfigDir
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
