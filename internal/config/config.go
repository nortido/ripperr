package config

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/sethvargo/go-envconfig"
)

func NormalizeClient(kind string) string {
	// env и старые настройки бывают в любом регистре и с пробелами,
	// а manager, settings и web сравнивают с "qbit" буквально
	return strings.ToLower(strings.TrimSpace(kind))
}

type Log struct {
	Level string `env:"LOG_LEVEL, default=info"`
}

type Manager struct {
	// "qbit", "transmission", "deluge" или "downloadstation"
	Client      string `env:"MANAGER_CLIENT, default=qbit"`
	QbitTorrent struct {
		Host     string `env:"QBIT_URL, default=localhost:8068"`
		Username string `env:"QBIT_USER, default=admin"`
		Password string `env:"QBIT_PASS, default=adminadmin"`
	}
	Transmission struct {
		Host string `env:"TRANSMISSION_URL, default=localhost:9091"`
		// у Transmission авторизацию часто выключают, так что пустые логин и пароль тут нормальный дефолт
		Username string `env:"TRANSMISSION_USER, default="`
		Password string `env:"TRANSMISSION_PASS, default="`
	}
	Deluge struct {
		Host string `env:"DELUGE_URL, default=localhost:8112"`
		// Web UI Deluge авторизует только паролем, логина у него нет
		Password string `env:"DELUGE_PASS, default=deluge"`
	}
	DownloadStation struct {
		Host     string `env:"DOWNLOADSTATION_URL, default=localhost:5000"`
		Username string `env:"DOWNLOADSTATION_USER, default="`
		Password string `env:"DOWNLOADSTATION_PASS, default="`
	}
	PausedMaxRatio        float64  `env:"MANAGER_PAUSED_MAX_RATIO, default=2"`
	DeleteEnabled         bool     `env:"MANAGER_DELETE_ENABLED, default=false"`
	DeleteFilesEnabled    bool     `env:"MANAGER_DELETE_FILES, default=true"`
	DeleteFilesCategories []string `env:"MANAGER_DELETE_FILES_CATEGORIES, default="`
	MinutesToReconnect    int      `env:"MANAGER_MINUTES_TO_RECONNECT, default=5"`
}

type TrackerItem struct {
	// дефолты объявляет пакет трекера в registry.Def: общий тип не может нести разный
	// default= на экземпляр. overwrite нужен, иначе go-envconfig пропустит уже заполненное поле
	Name         string   `json:"name" env:"NAME, overwrite"`
	Domain       string   `json:"domain" env:"DOMAIN, overwrite"`
	ActiveDomain string   `json:"active_domain"` // переопределение только из БД
	Mirrors      []string `json:"mirrors"`       // переопределение только из БД
	Retired      []string `json:"retired"`
	Unconfirmed  []string `json:"unconfirmed"`
	Credentials  string   `json:"credentials" env:"CREDENTIALS, overwrite"`
	Login        string   `json:"login"`
	Password     string   `json:"password"`
	Publicity    string   `json:"publicity" env:"PUBLICITY, overwrite"`
	LoginURL     string   `json:"login_url" env:"LOGIN_URL, overwrite"`
	DownloadURL  string   `json:"download_url" env:"DOWNLOAD_URL, overwrite"`
	HourLimit    int      `json:"hour_limit" env:"HOUR_LIMIT, overwrite"`
	ProxyType    int      `json:"proxy_type" env:"PROXY_TYPE, overwrite"`
	Cookies      string   `json:"cookies" env:"COOKIES, overwrite"`
	Enabled      bool     `json:"enabled" env:"ENABLED, overwrite"`
}

type Tracker map[string]*TrackerItem

func (t Tracker) ByKey(key string) *TrackerItem {
	return t[key]
}

func (t Tracker) Clone() Tracker {
	out := make(Tracker, len(t))
	for key, it := range t {
		item := *it
		item.Mirrors = append([]string(nil), it.Mirrors...)
		item.Retired = append([]string(nil), it.Retired...)
		item.Unconfirmed = append([]string(nil), it.Unconfirmed...)
		out[key] = &item
	}
	return out
}

func (t Tracker) Keys() []string {
	out := make([]string, 0, len(t))
	for k := range t {
		out = append(out, k)
	}
	// сортируем только ради повторяемости обхода, порядок в UI задает реестр
	sort.Strings(out)
	return out
}

func ApplyTrackerEnv(ctx context.Context, t Tracker, l envconfig.Lookuper) error {
	// ключи известны только в рантайме, envconfig.Process такой map сам не обойдет
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
	WatchTag              string `env:"RUNNER_TAG, default=watch"` // из окружения, действующий список в WatchTags
	WatchTags             []string
	WatchMode             string   // только из БД, пусто: режим выводится из списков
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
	MirrorConfirm         bool     `env:"RUNNER_MIRROR_CONFIRM, default=false"`
	RunOnStart            bool     `env:"RUNNER_RUN_ON_START, default=true"`
	Timezone              string   // переопределение только из БД, пусто: локальная зона процесса
}

var AllowedRefreshPerDay = []int{1, 2, 3, 4, 6, 8, 12, 24}

func (r Runner) ValidateSchedule() error {
	if !slices.Contains(AllowedRefreshPerDay, r.ListRefreshPerDay) {
		return fmt.Errorf("RUNNER_LIST_REFRESH_PER_DAY=%d must be one of %v", r.ListRefreshPerDay, AllowedRefreshPerDay)
	}
	if r.StartHour < 0 || r.StartHour > 23 {
		return fmt.Errorf("RUNNER_START_HOUR=%d must be 0..23", r.StartHour)
	}
	return nil
}

func (r Runner) Validate() error {
	// пустое присваивание (RUNNER_MAGNET_TIMEOUT= или пустая переменная в compose) дает 0,
	// это "не задано". buildTorrentFromMagnet сам поднимет его до минуты
	if r.MagnetTimeout == 0 {
		return nil
	}
	// из env значение приходит без границ, а битый таймаут вроде 1 молча жег бы слот
	// часового лимита трекера на каждой записи. Лучше упасть на старте
	if r.MagnetTimeout < 5 || r.MagnetTimeout > 600 {
		return fmt.Errorf("RUNNER_MAGNET_TIMEOUT=%d must be 5..600", r.MagnetTimeout)
	}
	return nil
}

type MirrorSource struct {
	Type   string `env:"MIRROR_SOURCE, default=github"`
	URL    string `env:"MIRROR_SOURCE_URL, default="`
	Direct bool   `env:"MIRROR_SOURCE_DIRECT, default=false"`
}

type Flaresolverr struct {
	Host        string `env:"FLARE_URL, default=localhost:8191/v1"`
	MaxTimeout  int    `env:"FLARE_TIMEOUT, default=300000"`
	CaptureHost string `env:"TLS_CAPTURE_HOST, default="`
	CapturePort int    `env:"TLS_CAPTURE_PORT, default=0"`
}

type Storage struct {
	// пусто: DBPath() выведет путь из ConfigDir
	Path string `env:"DB_PATH, default="`
}

type Notifications struct {
	TelegramToken  string `env:"NOTIFY_TELEGRAM_TOKEN, default="`
	TelegramChatID string `env:"NOTIFY_TELEGRAM_CHAT_ID, default="`
	WebhookURL     string `env:"NOTIFY_WEBHOOK_URL, default="`
	Enabled        bool   `env:"NOTIFY_ENABLED, default=true"`

	// без env: задаются только через страницу интеграций и лежат в БД
	// нулевое значение = канал включен, так старые БД и env-only настройки не меняются
	TelegramDisabled bool
	WebhookDisabled  bool
	EmailDisabled    bool
	PushoverDisabled bool
	EmailHost        string
	EmailPort        int
	EmailSecurity    string
	EmailUsername    string
	EmailPassword    string
	EmailFrom        string
	EmailTo          string
	PushoverToken    string
	PushoverUser     string
	BrowserDisabled  bool
	Language         string

	Events []string

	// тот же SAFE_MODE, что у App.SafeMode: Notify.Silent берется из env без доп. проводки в main
	Silent bool `env:"SAFE_MODE, default=false"`
}
