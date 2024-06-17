package config

type Manager struct {
	QbitTorrent struct {
		Host     string `env:"QBIT_URL, default=http://192.168.1.200:8068"`
		Username string `env:"QBIT_USER, default=admin"`
		Password string `env:"QBIT_PASS, default=adminadmin"`
	}
	//TODO: delete files for specific category only
	AllowDeleteFiles bool `env:"MANAGER_ALLOW_DELETE_FILES, default=false"`
}

type Runner struct {
	ProcessSecondsDelay int    `env:"RUNNER_PROCESS_SECONDS_DELAY, default=60"`
	RunsPerDay          int    `env:"RUNNER_PER_DAY, default=24"`
	WatchTag            string `env:"RUNNER_TAG, default=watch"`
	UpdateThreshold     int64  `env:"RUNNER_UPDATE_THRESHOLD, default=43200"`
	HttpTimeout         int64  `env:"RUNNER_HTTP_TIMEOUT, default=30"`
	ConnMaxRetries      int    `env:"RUNNER_CONN_MAX_RETRIES, default=3"`
	ConnMinutesToRetry  int    `env:"RUNNER_CONN_MINUTES_TO_RETRY, default=10"`
}

type Flaresolverr struct {
	Host       string `env:"FLARE_URL, default=http://192.168.1.200:8191/v1"`
	MaxTimeout int    `env:"FLARE_TIMEOUT, default=30000"`
}
