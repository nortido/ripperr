package model

import "net/http"

type ProxyRequestOptions struct {
	Cookies  []*http.Cookie
	Session  string
	WithBody bool
	Destroy  bool
}
