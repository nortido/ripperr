package config

type Manager struct {
	QbitTorrent struct {
		Host     string `env:"QBIT_URL, default=http://192.168.1.200:8068"`
		Username string `env:"QBIT_USER, default=admin"`
		Password string `env:"QBIT_PASS, default=adminadmin"`
	}
	DeleteFilesEnabled    bool     `env:"MANAGER_DELETE_FILES, default=false"`
	DeleteFilesCategories []string `env:"MANAGER_DELETE_FILES_CATEGORIES, default=switch,files"`
}

type Runner struct {
	ProcessDelay       int    `env:"RUNNER_PROCESS_DELAY, default=20"`
	ProcessSizeLimit   int    `env:"RUNNER_PROCESS_SIZE_LIMIT, default=5"`
	WatchTag           string `env:"RUNNER_TAG, default=watch"`
	UpdateThreshold    int64  `env:"RUNNER_UPDATE_THRESHOLD, default=1440"`
	HttpTimeout        int64  `env:"RUNNER_HTTP_TIMEOUT, default=60"`
	ConnMaxRetries     int    `env:"RUNNER_CONN_MAX_RETRIES, default=3"`
	ConnMinutesToRetry int    `env:"RUNNER_CONN_MINUTES_TO_RETRY, default=1440"`
}

type Flaresolverr struct {
	Host       string `env:"FLARE_URL, default=http://192.168.1.200:8191/v1"`
	MaxTimeout int    `env:"FLARE_TIMEOUT, default=60000"`
}

type Database struct {
	MasterURL      string `env:"DB_URL, default=postgres://postgres:PLACEHOLDER@192.168.1.200:5432/trackerr"`
	ConnectTimeout int    `env:"DB_CONNECT_TIMEOUT, default=60"`
}
