package config

type Log struct {
	Level string `env:"LOG_LEVEL, default=error"`
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

type Tracker struct {
	Rutracker struct {
		Name        string `env:"TRACKER_RUTRACKER_NAME, default=rutracker"`
		Domain      string `env:"TRACKER_RUTRACKER_DOMAIN, default=rutracker.org"`
		Credentials string `env:"TRACKER_RUTRACKER_CREDENTIALS, default="`
		Publicity   string `env:"TRACKER_RUTRACKER_PUBLICITY, default=private"`
		LoginURL    string `env:"TRACKER_RUTRACKER_LOGIN_URL, default=https://rutracker.org/forum/login.php"`
		DownloadURL string `env:"TRACKER_RUTRACKER_DOWNLOAD_URL, default=https://rutracker.org/forum/dl.php?t="`
		HourLimit   int    `env:"TRACKER_RUTRACKER_HOUR_LIMIT, default=20"`
		ProxyType   int    `env:"TRACKER_RUTRACKER_PROXY_TYPE, default=1"`
		Enabled     bool   `env:"TRACKER_RUTRACKER_ENABLED, default=true"`
	}
	Kinozal struct {
		Name        string `env:"TRACKER_KINOZAL_NAME, default=kinozal"`
		Domain      string `env:"TRACKER_KINOZAL_DOMAIN, default=kinozal.tv"`
		Credentials string `env:"TRACKER_KINOZAL_CREDENTIALS, default="`
		Publicity   string `env:"TRACKER_KINOZAL_PUBLICITY, default=private"`
		LoginURL    string `env:"TRACKER_KINOZAL_LOGIN_URL, default=https://kinozal.tv/takelogin.php"`
		DownloadURL string `env:"TRACKER_KINOZAL_DOWNLOAD_URL, default=https://kinozal.tv/download.php?id="`
		HourLimit   int    `env:"TRACKER_KINOZAL_HOUR_LIMIT, default=30"`
		ProxyType   int    `env:"TRACKER_KINOZAL_PROXY_TYPE, default=1"`
		Cookies     string `env:"TRACKER_KINOZAL_COOKIES, default="`
		Enabled     bool   `env:"TRACKER_KINOZAL_ENABLED, default=true"`
	}
	Rutor struct {
		Name        string `env:"TRACKER_RUTOR_NAME, default=rutor"`
		Domain      string `env:"TRACKER_RUTOR_DOMAIN, default=rutor.is"`
		Publicity   string `env:"TRACKER_RUTOR_PUBLICITY, default=public"`
		DownloadURL string `env:"TRACKER_RUTOR_DOWNLOAD_URL, default=https://d.rutor.info/download/"`
		HourLimit   int    `env:"TRACKER_RUTOR_HOUR_LIMIT, default=30"`
		Enabled     bool   `env:"TRACKER_RUTOR_ENABLED, default=true"`
	}
	}
	NNM struct {
		Name        string `env:"TRACKER_NNM_NAME, default=nnm"`
		Domain      string `env:"TRACKER_NNM_DOMAIN, default=nnmclub.to"`
		Credentials string `env:"TRACKER_NNM_CREDENTIALS, default="`
		Publicity   string `env:"TRACKER_NNM_PUBLICITY, default=private"`
		LoginURL    string `env:"TRACKER_NNM_LOGIN_URL, default=https://nnmclub.to/forum/login.php"`
		DownloadURL string `env:"TRACKER_NNM_DOWNLOAD_URL, default=https://nnmclub.to/forum/download.php?id="`
		HourLimit   int    `env:"TRACKER_NNM_HOUR_LIMIT, default=25"`
		Cookies     string `env:"TRACKER_NNM_COOKIES, default="`
		ProxyType   int    `env:"TRACKER_NNM_PROXY_TYPE, default=1"`
		Enabled     bool   `env:"TRACKER_NNM_ENABLED, default=true"`
	}
	TeamHD struct {
		Name        string `env:"TRACKER_TEAMHD_NAME, default=teamhd"`
		Domain      string `env:"TRACKER_TEAMHD_DOMAIN, default=teamhd.org"`
		Publicity   string `env:"TRACKER_TEAMHD_PUBLICITY, default=private"`
		DownloadURL string `env:"TRACKER_TEAMHD_DOWNLOAD_URL, default=https://teamhd.org/download.php?id="`
		HourLimit   int    `env:"TRACKER_TEAMHD_HOUR_LIMIT, default=25"`
		Cookies     string `env:"TRACKER_TEAMHD_COOKIES, default="`
		Enabled     bool   `env:"TRACKER_TEAMHD_ENABLED, default=true"`
	}
}

type Runner struct {
	WorkerDelay           int      `env:"RUNNER_WORKER_DELAY, default=20"`
	WatchTag              string   `env:"RUNNER_TAG, default=watch"`
	MonthThresholdDays    int      `env:"RUNNER_FIRST_MONTH_UPDATE_THRESHOLD, default=1"`
	HalfYearThresholdDays int      `env:"RUNNER_HALF_YEAR_UPDATE_THRESHOLD, default=7"`
	YearThresholdDays     int      `env:"RUNNER_YEAR_UPDATE_THRESHOLD, default=30"`
	ListRefreshPerDay     int      `env:"RUNNER_LIST_REFRESH_PER_DAY, default=2"`
	HttpTimeout           int64    `env:"RUNNER_HTTP_TIMEOUT, default=30"`
	CBRetries             int      `env:"RUNNER_CB_RETRIES, default=2"`
	CBMinutesToRetry      int      `env:"RUNNER_CB_MINUTES_TO_RETRY, default=60"`
	AllowedCategories     []string `env:"RUNNER_ALLOWED_CATEGORIES, default="`
}

type Flaresolverr struct {
	Host       string `env:"FLARE_URL, default=localhost:8191/v1"`
	MaxTimeout int    `env:"FLARE_TIMEOUT, default=300000"`
}

type Storage struct {
	MasterURL      string `env:"DB_URL, default=postgres://user:password@localhost:5432/trackerr"`
	ConnectTimeout int    `env:"DB_CONNECT_TIMEOUT, default=60"`
}
