package config

// App is the root env-backed configuration.
type App struct {
	Log          Log
	Runner       Runner
	Tracker      Tracker
	Manager      Manager
	Flaresolverr Flaresolverr
	DB           Storage
	Web          Web
}
