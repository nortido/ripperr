package main

import (
	"context"
	"database/sql"
	"encoding/json"
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
	"ripperr/internal/webpush"
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
	baseCfg.Runner.WatchTags = settings.WatchTagsFromEnv(baseCfg.Runner.WatchTag)
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
	// defer-ы идут в обратном порядке: сначала остановится web, потом сброс очереди событий, потом закроется БД
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.FlushEvents(flushCtx); err != nil {
			logger.Warn("events: flush at exit: " + err.Error())
		}
	}()

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

	reload := newReload(runnerState, reloadCh)

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
		n := notify.New(notify.Config(ecfg.Notify), ecfg.Runner.DownloadProxy).WithAppURL(ecfg.Web.AppURL).WithPush(webpush.NewStore(db)).WithEvents(db)
		if open {
			n.Notify(notify.TrackerDownEvent(tracker, errMsg))
		} else {
			n.Notify(notify.TrackerUpEvent(tracker))
		}
	})

	var webSrv *webpkg.Server
	if baseCfg.Web.Enabled {
		webSrv = webpkg.New(db, reload, effective, baseCfg.Web.Listen, runnerState, breakerAlerts)
		webSrv.SetEvents(db)
		webSrv.SetStrictEffective(func() (config.App, error) {
			cfg := baseCfg
			var invalid *settings.InvalidConfigError
			if err := settings.ApplyOverrides(context.Background(), &cfg, db); err != nil && !errors.As(err, &invalid) {
				return config.App{}, err
			}
			return cfg, nil
		})
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
		cfg, ok := overlaidConfig(ctx, baseCfg, db, reloadCh, runnerState)
		if !ok {
			logger.Info("goodbye")
			return
		}
		// env проверяем после наложения БД: битый env не мешает, если его перекрывает сохраненное
		if err := validateEffective(baseCfg, cfg); err != nil {
			logger.Error(err.Error())
			return
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
			Immediate:            first && cfg.Runner.RunOnStart,
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

var overrideRetryBase = 5 * time.Second

const overrideRetryMax = 5 * time.Minute

type overrideReader interface {
	GetSetting(ctx context.Context, key string) (json.RawMessage, bool, error)
}

func overlaidConfig(ctx context.Context, base config.App, db overrideReader, reloadCh <-chan struct{}, state *runner.State) (config.App, bool) {
	// без настроек из БД раннер пошел бы с env-дефолтами (удаление файлов включено, убранные
	// трекеры снова в работе), поэтому ждем чтения, а не берем дефолты
	defer state.SetWaitingSettings(false)
	for attempt := 0; ; attempt++ {
		cfg := base // копии по значению хватает: overlay срезы в секциях всегда переприсваивает
		err := settings.ApplyOverrides(ctx, &cfg, db)
		if err == nil {
			return cfg, true
		}
		var invalid *settings.InvalidConfigError
		if errors.As(err, &invalid) {
			logger.Error(err.Error())
			return cfg, true
		}
		if ctx.Err() != nil {
			return cfg, false
		}
		wait := overrideRetryMax
		if attempt < 20 {
			wait = min(overrideRetryBase<<attempt, overrideRetryMax)
		}
		state.SetWaitingSettings(true)
		logger.Error("settings: cannot read stored settings, runner not started, retry in " + wait.String() + ": " + err.Error())
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return cfg, false
		case <-reloadCh:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func newReload(state *runner.State, ch chan<- struct{}) func() {
	return func() {
		// только обычный проход: форс-проверку и пробный прогон сохранение не прерывает,
		// а останавливаемый проход перезапускать незачем
		if state.NormalRunning() && !state.DryRunning() && !state.Stopping() {
			state.MarkRestart()
		}
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func validateEffective(base, cfg config.App) error {
	if err := cfg.Runner.ValidateSchedule(); err != nil {
		return err
	}
	if err := cfg.Runner.Validate(); err != nil {
		return err
	}
	// адрес из БД проверен при сохранении, из env приходит как есть: без схемы
	// ProxyTransport молча пошел бы напрямую. Битый сохраненный руками не трогаем, как раньше
	if cfg.Runner.DownloadProxy == base.Runner.DownloadProxy {
		if err := settings.ValidateDownloadProxy(cfg.Runner.DownloadProxy); err != nil {
			return errors.New("RUNNER_DOWNLOAD_PROXY: " + err.Error())
		}
	}
	// как прокси: env проверяем, только пока он в силе. Direct не в счет
	if cfg.MirrorSource.Type == base.MirrorSource.Type && strings.TrimSpace(cfg.MirrorSource.URL) == strings.TrimSpace(base.MirrorSource.URL) {
		if err := settings.ValidateMirrorSource(cfg.MirrorSource); err != nil {
			return errors.New("MIRROR_SOURCE: " + err.Error())
		}
	}
	return nil
}
