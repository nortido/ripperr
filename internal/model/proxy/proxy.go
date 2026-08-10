package proxy

import "net/http"

const (
	TypeNone = iota
	TypeFlare
)

type ProxyType uint8

type Config struct {
	Flaresolverr ItemConfig
}

type ItemConfig struct {
	URL        string
	MaxTimeout int
}

type Options struct {
	Cookies     []*http.Cookie
	Session     string
	OnlyCookies bool
}
