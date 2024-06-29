package tracker

import (
	"net/http"
)

const (
	PublicityPrivate = "private"
	PublicitySemi    = "semi"
	PublicityPublic  = "public"
)

type Tracker struct {
	TypeID      int
	Name        string
	Host        string
	Credentials string
	ProxyType   int
	Publicity   string
	LoginURL    string
	DownloadURL string
	Cookies     []*http.Cookie
	Exists      bool
}
