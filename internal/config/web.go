package config

type Web struct {
	Listen       string   `env:"WEB_LISTEN, default=:6410"`
	Enabled      bool     `env:"WEB_ENABLED, default=true"`
	TrustedProxy bool     `env:"WEB_TRUSTED_PROXY, default=false"`
	AllowedHosts []string `env:"WEB_ALLOWED_HOSTS"`
	AppURL       string   `env:"WEB_APP_URL"`
	PortLocked   bool     `env:"RIPPERR_IN_DOCKER, default=false"`
}
