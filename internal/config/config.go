package config

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sethvargo/go-envconfig"
)

// NormalizeClient приводит значение MANAGER_CLIENT (env, .env, БД настроек) к
// одному каноническому виду в одном месте: env-переменные и старые записи
// настроек попадаются в любом регистре и с пробелами (MANAGER_CLIENT=Transmission),
// а manager, settings и web сравнивают его буквально с "qbit"/"transmission",
// поэтому без нормализации такое значение уходит не в ту ветку.
func NormalizeClient(kind string) string {
	return strings.ToLower(strings.TrimSpace(kind))
}

type Log struct {
	Level string `env:"LOG_LEVEL, default=info"`
}

type Manager struct {
	// Client выбирает активный клиент загрузок: "qbit" или "transmission"
	Client      string `env:"MANAGER_CLIENT, default=qbit"`
	QbitTorrent struct {
		Host     string `env:"QBIT_URL, default=localhost:8068"`
		Username string `env:"QBIT_USER, default=admin"`
		Password string `env:"QBIT_PASS, default=adminadmin"`
	}
	Transmission struct {
		Host string `env:"TRANSMISSION_URL, default=localhost:9091"`
		// у Transmission аутентификация часто выключена, поэтому пустые логин
		// и пароль это нормальное значение по умолчанию, в отличие от qbit
		Username string `env:"TRANSMISSION_USER, default="`
		Password string `env:"TRANSMISSION_PASS, default="`
	}
	PausedMaxRatio        float64  `env:"MANAGER_PAUSED_MAX_RATIO, default=2"`
	DeleteEnabled         bool     `env:"MANAGER_DELETE_ENABLED, default=true"`
	DeleteFilesEnabled    bool     `env:"MANAGER_DELETE_FILES, default=true"`
	DeleteFilesCategories []string `env:"MANAGER_DELETE_FILES_CATEGORIES, default="`
	MinutesToReconnect    int      `env:"MANAGER_MINUTES_TO_RECONNECT, default=5"`
}

// TrackerItem это настройки одного трекера. Имена переменных окружения на трекер
// собирает ApplyTrackerEnv из ключа (TRACKER_<KEY>_*); значения по умолчанию объявляет
// сам пакет трекера в registry.Def, потому что общий тип не может нести разный
// `default=` на экземпляр. `overwrite` даёт переменной окружения победить
// предзаполненное значение (иначе go-envconfig пропускает поле, которое уже не нулевое).
type TrackerItem struct {
	Name         string   `json:"name" env:"NAME, overwrite"`
	Domain       string   `json:"domain" env:"DOMAIN, overwrite"`
	ActiveDomain string   `json:"active_domain"` // переопределение только из БД
	Mirrors      []string `json:"mirrors"`       // переопределение только из БД
	Credentials  string   `json:"credentials" env:"CREDENTIALS, overwrite"`
	Publicity    string   `json:"publicity" env:"PUBLICITY, overwrite"`
	LoginURL     string   `json:"login_url" env:"LOGIN_URL, overwrite"`
	DownloadURL  string   `json:"download_url" env:"DOWNLOAD_URL, overwrite"`
	HourLimit    int      `json:"hour_limit" env:"HOUR_LIMIT, overwrite"`
	ProxyType    int      `json:"proxy_type" env:"PROXY_TYPE, overwrite"`
	Cookies      string   `json:"cookies" env:"COOKIES, overwrite"`
	Enabled      bool     `json:"enabled" env:"ENABLED, overwrite"`
}

// Tracker это набор настроек по ключу трекера. Состав набора задаёт реестр
// (internal/tracker/registry), config о конкретных трекерах не знает: новый
// трекер регистрирует себя сам и появляется здесь без правки этого файла.
type Tracker map[string]*TrackerItem

func (t Tracker) ByKey(key string) *TrackerItem {
	return t[key]
}

// Clone нужен там, где App копируется по значению: набор это map, и без
// собственной копии оверлей из БД писал бы в базовый конфиг навсегда.
func (t Tracker) Clone() Tracker {
	out := make(Tracker, len(t))
	for key, it := range t {
		item := *it
		item.Mirrors = append([]string(nil), it.Mirrors...)
		out[key] = &item
	}
	return out
}

// Keys отдаёт ключи в алфавитном порядке. Порядок для показа в UI задаёт реестр
// (по id), здесь важна только повторяемость обхода.
func (t Tracker) Keys() []string {
	out := make([]string, 0, len(t))
	for k := range t {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ApplyTrackerEnv накладывает переменные окружения на каждый трекер набора.
// Отдельным проходом, а не тегом prefix на поле: имена ключей известны только в
// рантайме, envconfig.Process сам такой набор не обойдёт.
func ApplyTrackerEnv(ctx context.Context, t Tracker, l envconfig.Lookuper) error {
	for _, key := range t.Keys() {
		prefix := "TRACKER_" + strings.ToUpper(key) + "_"
		if err := envconfig.ProcessWith(ctx, &envconfig.Config{
			Target:   t[key],
			Lookuper: envconfig.PrefixLookuper(prefix, l),
		}); err != nil {
			return fmt.Errorf("tracker %s: %w", key, err)
		}
	}
	return nil
}

type Runner struct {
	WatchTag              string `env:"RUNNER_TAG, default=watch"` // источник из окружения; действующий список в WatchTags
	WatchTags             []string
	MonthThresholdDays    int      `env:"RUNNER_FIRST_MONTH_UPDATE_THRESHOLD, default=1"`
	HalfYearThresholdDays int      `env:"RUNNER_HALF_YEAR_UPDATE_THRESHOLD, default=7"`
	YearThresholdDays     int      `env:"RUNNER_YEAR_UPDATE_THRESHOLD, default=30"`
	ErrorStreakThreshold  int      `env:"RUNNER_ERROR_STREAK_THRESHOLD, default=3"`
	ListRefreshPerDay     int      `env:"RUNNER_LIST_REFRESH_PER_DAY, default=2"`
	StartHour             int      `env:"RUNNER_START_HOUR, default=7"`
	HttpTimeout           int64    `env:"RUNNER_HTTP_TIMEOUT, default=30"`
	DownloadTimeout       int64    `env:"RUNNER_DOWNLOAD_TIMEOUT, default=240"`
	DownloadProxy         string   `env:"RUNNER_DOWNLOAD_PROXY, default="`
	TLSProfile            string   `env:"RUNNER_TLS_PROFILE, default=chrome_131"`
	CBRetries             int      `env:"RUNNER_CB_RETRIES, default=2"`
	CBMinutesToRetry      int      `env:"RUNNER_CB_MINUTES_TO_RETRY, default=60"`
	AllowedCategories     []string `env:"RUNNER_ALLOWED_CATEGORIES, default="`
	MagnetTimeout         int64    `env:"RUNNER_MAGNET_TIMEOUT, default=60"`
	Timezone              string   // переопределение только из БД; empty = process local
}

// Validate проверяет значения Runner, которые envconfig.Process берёт из
// окружения как есть, без собственных границ (в отличие от того же поля из
// БД: там ValidateSchedulePatch/ValidateSchedule режут 5..600). Битый
// RUNNER_MAGNET_TIMEOUT (например, "1") иначе молча жжёт слот часового
// лимита трекера на каждой записи без единой ошибки в логе; валим старт, как
// главная функция уже поступает с ошибкой envconfig.Process, а не тихо
// подгоняем значение к границе - неверный .env лучше увидеть сразу.
// 0 не валим: go-envconfig подставляет default=60 только когда переменной нет
// вовсе, а пустое присваивание (RUNNER_MAGNET_TIMEOUT= или ссылка на
// незаданную переменную в compose) даёт found=true, val="" - то есть 0, и это
// "не задано", а не мусор. handler.buildTorrentFromMagnet сам клампит такой
// 0 до минуты, как и раньше
func (r Runner) Validate() error {
	if r.MagnetTimeout == 0 {
		return nil
	}
	if r.MagnetTimeout < 5 || r.MagnetTimeout > 600 {
		return fmt.Errorf("RUNNER_MAGNET_TIMEOUT=%d must be 5..600", r.MagnetTimeout)
	}
	return nil
}

type Flaresolverr struct {
	Host       string `env:"FLARE_URL, default=localhost:8191/v1"`
	MaxTimeout int    `env:"FLARE_TIMEOUT, default=300000"`
}

type Storage struct {
	// пусто означает, что DBPath() выведет его из ConfigDir
	Path string `env:"DB_PATH, default="`
}

type Notifications struct {
	TelegramToken  string `env:"NOTIFY_TELEGRAM_TOKEN, default="`
	TelegramChatID string `env:"NOTIFY_TELEGRAM_CHAT_ID, default="`
	WebhookURL     string `env:"NOTIFY_WEBHOOK_URL, default="`
	Enabled        bool   `env:"NOTIFY_ENABLED, default=true"`
}
