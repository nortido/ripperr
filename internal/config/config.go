package config

type Manager struct {
	QbitTorrent struct {
		Host     string `env:"QBIT_URL, default=http://192.168.1.200:8068"`
		Username string `env:"QBIT_USER, default=admin"`
		Password string `env:"QBIT_PASS, default=adminadmin"`
	}
}

type Runner struct {
	MaxTimeout int    `env:"RUNNER_TIMEOUT, default=60"`
	RunsPerDay int    `env:"RUNNER_PER_DAY, default=6"`
	WatchTag   string `env:"RUNNER_TAG, default=watch"`
}

type Flaresolverr struct {
	Host       string `env:"FLARE_URL, default=http://192.168.1.200:8191/v1"`
	MaxTimeout int    `env:"FLARE_TIMEOUT, default=60000"`
}
