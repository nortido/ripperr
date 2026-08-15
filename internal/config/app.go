package config

type App struct {
	Log          Log
	Runner       Runner
	Tracker      Tracker
	Manager      Manager
	Flaresolverr Flaresolverr
	DB           Storage
	Web          Web
	Notify       Notifications
}
