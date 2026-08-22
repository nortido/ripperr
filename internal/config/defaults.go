package config

// DefaultApp возвращает конфиг с предзаполненными общими значениями; набор трекеров
// пуст, его заполняет вызывающий из реестра (internal/tracker/registry).
func DefaultApp() App {
	return App{Tracker: Tracker{}}
}
