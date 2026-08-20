package config

import "path/filepath"

const DefaultConfigDir = "/config"

type App struct {
	ConfigDir string `env:"CONFIG_DIR, default=/config"`
	// BlackholeDir переопределяет, где живёт blackhole; пусто означает
	// подкаталог по умолчанию внутри ConfigDir (см. BlackholeDirPath).
	BlackholeDir string `env:"BLACKHOLE_DIR, default="`
	Log          Log
	Runner       Runner
	Tracker      Tracker
	Manager      Manager
	Flaresolverr Flaresolverr
	DB           Storage
	Web          Web
	Notify       Notifications
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
	return filepath.Join(a.ConfigDir, "trackerr.db")
}
