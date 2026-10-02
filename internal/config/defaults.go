package config

func DefaultApp() App {
	// трекеры потом заполняет вызывающий из internal/tracker/registry
	return App{Tracker: Tracker{}}
}
