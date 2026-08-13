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

// Clone returns a shallow copy. Struct is copied by value; slice fields (in nested sections)
// are shared but never mutated in place—overlays always assign fresh slices.
func (a App) Clone() App {
	return a
}
