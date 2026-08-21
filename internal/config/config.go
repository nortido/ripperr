package config

import "strings"

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
// берутся из префикса на каждом поле Tracker; значения по умолчанию лежат в DefaultApp,
// потому что общий тип не может нести разный `default=` на экземпляр. `overwrite` даёт
// переменной окружения победить предзаполненное значение (иначе go-envconfig пропускает
// поле, которое уже не нулевое).
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

type Tracker struct {
	Rutracker TrackerItem `env:", prefix=TRACKER_RUTRACKER_"`
	Kinozal   TrackerItem `env:", prefix=TRACKER_KINOZAL_"`
	Rutor     TrackerItem `env:", prefix=TRACKER_RUTOR_"`
	NNM       TrackerItem `env:", prefix=TRACKER_NNM_"`
	TeamHD    TrackerItem `env:", prefix=TRACKER_TEAMHD_"`
}

var TrackerKeys = []string{"rutracker", "kinozal", "rutor", "teamhd", "nnm"}

func (t *Tracker) ByKey(key string) *TrackerItem {
	switch key {
	case "rutracker":
		return &t.Rutracker
	case "kinozal":
		return &t.Kinozal
	case "rutor":
		return &t.Rutor
	case "teamhd":
		return &t.TeamHD
	case "nnm":
		return &t.NNM
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
