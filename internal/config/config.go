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
	WorkerDelay      int    `env:"RUNNER_WORKER_DELAY, default=60"`
	WorkerChunkLimit int    `env:"RUNNER_WORKER_CHUNK_LIMIT, default=7"`
	WatchTag         string `env:"RUNNER_TAG, default=watch"`
	UpdateThreshold  int64  `env:"RUNNER_UPDATE_THRESHOLD, default=1440"`
	HttpTimeout      int64  `env:"RUNNER_HTTP_TIMEOUT, default=30"`
	CBRetries        int    `env:"RUNNER_CB_RETRIES, default=2"`
	CBMinutesToRetry int    `env:"RUNNER_CB_MINUTES_TO_RETRY, default=30"`
}

type Flaresolverr struct {
	Host       string `env:"FLARE_URL, default=http://192.168.1.200:8191/v1"`
	MaxTimeout int    `env:"FLARE_TIMEOUT, default=300000"`
}

type Storage struct {
	MasterURL      string `env:"DB_URL, default=postgres://postgres:PLACEHOLDER@192.168.1.200:5432/trackerr"`
	ConnectTimeout int    `env:"DB_CONNECT_TIMEOUT, default=60"`
}
