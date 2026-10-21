package config

import "path/filepath"

const DefaultConfigDir = "/config"

type App struct {
	ConfigDir string `env:"CONFIG_DIR, default=/config"`
	// пусто: подкаталог внутри ConfigDir, см. BlackholeDirPath
	BlackholeDir string `env:"BLACKHOLE_DIR, default="`
	Log          Log
	Runner       Runner
	Tracker      Tracker
	Manager      Manager
	Flaresolverr Flaresolverr
	MirrorSource MirrorSource
	DB           Storage
	Web          Web
	Notify       Notifications
	Metrics      Metrics
}

func (a App) BlackholeDirPath() string {
	if a.BlackholeDir != "" {
		return a.BlackholeDir
	}
	return filepath.Join(a.ConfigDir, "blackhole")
}

func (a App) DBPath() string {
	if a.DB.Path != "" {
		return a.DB.Path
	}
	return filepath.Join(a.ConfigDir, "ripperr.db")
}

func (a App) SecretsDir() string {
	// ключ лежит рядом с БД: при DB_PATH на отдельном томе он не теряется вместе с /config
	return filepath.Dir(a.DBPath())
}

type Metrics struct {
	// без env: токен задается только на странице интеграций и лежит в БД
	Token string
}
