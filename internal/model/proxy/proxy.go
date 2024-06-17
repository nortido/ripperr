package proxy

import "net/http"

const (
	TypeNone = iota
	TypeFlare
)

type ProxyType uint8

type Config struct {
	None         ItemConfig
	Flaresolverr ItemConfig
	Http         ItemConfig
}

type ItemConfig struct {
	URL        string
	Username   string
	Password   string
	MaxTimeout int
}

type Options struct {
	Cookies     []*http.Cookie
	Session     string
	OnlyCookies bool
}
