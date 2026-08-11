package config

type Web struct {
	Listen  string `env:"WEB_LISTEN, default=:6410"`
	Enabled bool   `env:"WEB_ENABLED, default=true"`
}
