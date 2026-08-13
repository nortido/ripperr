package config

type Web struct {
	Listen       string `env:"WEB_LISTEN, default=:6410"`
	Enabled      bool   `env:"WEB_ENABLED, default=true"`
	LanBypass    bool   `env:"WEB_LAN_BYPASS, default=true"`
	TrustedProxy bool   `env:"WEB_TRUSTED_PROXY, default=false"`
}
