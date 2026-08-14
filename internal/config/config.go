package config

type Log struct {
	Level string `env:"LOG_LEVEL, default=info"`
}

type Manager struct {
	QbitTorrent struct {
		Host     string `env:"QBIT_URL, default=localhost:8068"`
		Username string `env:"QBIT_USER, default=admin"`
		Password string `env:"QBIT_PASS, default=adminadmin"`
	}
	PausedMaxRatio        float64  `env:"MANAGER_PAUSED_MAX_RATIO, default=2"`
	DeleteEnabled         bool     `env:"MANAGER_DELETE_ENABLED, default=true"`
	DeleteFilesEnabled    bool     `env:"MANAGER_DELETE_FILES, default=true"`
	DeleteFilesCategories []string `env:"MANAGER_DELETE_FILES_CATEGORIES, default="`
	MinutesToReconnect    int      `env:"MANAGER_MINUTES_TO_RECONNECT, default=5"`
}

// TrackerItem is one tracker's settings. Per-tracker env names come from the
// prefix on each Tracker field; the defaults live in DefaultApp because a shared
// type cannot carry a different `default=` per instance. `overwrite` is what lets
// an env var win over the pre-filled default (go-envconfig otherwise skips a
// field that is already non-zero).
type TrackerItem struct {
	Name         string   `json:"name" env:"NAME, overwrite"`
	Domain       string   `json:"domain" env:"DOMAIN, overwrite"`
	ActiveDomain string   `json:"active_domain"` // DB-only override
	Mirrors      []string `json:"mirrors"`       // DB-only override
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
	WatchTag              string `env:"RUNNER_TAG, default=watch"` // env source; effective list in WatchTags
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
	Timezone              string   // DB-only override; empty = process local
}

type Flaresolverr struct {
	Host       string `env:"FLARE_URL, default=localhost:8191/v1"`
	MaxTimeout int    `env:"FLARE_TIMEOUT, default=300000"`
}

type Storage struct {
	MasterURL      string `env:"DB_URL, default=postgres://user:password@localhost:5432/trackerr"`
	ConnectTimeout int    `env:"DB_CONNECT_TIMEOUT, default=60"`
}
