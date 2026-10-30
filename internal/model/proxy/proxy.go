package proxy

import "net/http"

const (
	TypeNone = iota
	TypeFlare
	TypeTLS
)

type ProxyType uint8

type Config struct {
	Flaresolverr ItemConfig
}

type ItemConfig struct {
	URL         string
	MaxTimeout  int
	CaptureHost string
	CapturePort int
}

type Options struct {
	Cookies     []*http.Cookie
	Session     string
	OnlyCookies bool
}
