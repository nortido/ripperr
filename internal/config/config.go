package config

type Manager struct {
	QbitTorrent struct {
		Host     string `env:"QBIT_URL, default=http://192.168.1.200:8068"`
		Username string `env:"QBIT_USER, default=admin"`
		Password string `env:"QBIT_PASS, default=adminadmin"`
	}
	AllowDeleteFiles bool `env:"MANAGER_ALLOW_DELETE_FILES, default=false"`
}

type Runner struct {
	ProcessTimeout  int    `env:"RUNNER_PROCESS_TIMEOUT, default=60"`
	RunsPerDay      int    `env:"RUNNER_PER_DAY, default=6"`
	WatchTag        string `env:"RUNNER_TAG, default=watch"`
	UpdateThreshold int64  `env:"RUNNER_UPDATE_THRESHOLD, default=43200"`
}

type Flaresolverr struct {
	Host       string `env:"FLARE_URL, default=http://192.168.1.200:8191/v1"`
	MaxTimeout int    `env:"FLARE_TIMEOUT, default=10000"`
}
