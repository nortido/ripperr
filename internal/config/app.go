package config

// App is the root env-backed configuration.
type App struct {
	Log          *Log
	Runner       *Runner
	Tracker      *Tracker
	Manager      *Manager
	Flaresolverr *Flaresolverr
	DB           *Storage
	Web          *Web
}

func (a App) Clone() App {
	c := App{}
	if a.Log != nil {
		v := *a.Log
		c.Log = &v
	}
	if a.Runner != nil {
		v := *a.Runner
		c.Runner = &v
	}
	if a.Tracker != nil {
		v := *a.Tracker
		c.Tracker = &v
	}
	if a.Manager != nil {
		v := *a.Manager
		c.Manager = &v
	}
	if a.Flaresolverr != nil {
		v := *a.Flaresolverr
		c.Flaresolverr = &v
	}
	if a.DB != nil {
		v := *a.DB
		c.DB = &v
	}
	if a.Web != nil {
		v := *a.Web
		c.Web = &v
	}
	return c
}
