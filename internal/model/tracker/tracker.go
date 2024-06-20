package tracker

import (
	"net/http"
)

type Tracker struct {
	TypeID      int
	Name        string
	Host        string
	Credentials string
	Cookies     []*http.Cookie
	ProxyType   int
	Exists      bool
}
