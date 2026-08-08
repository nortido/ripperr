package helper

import "net/http"

// HTTPDoer is satisfied by *http.Client and TLS download clients.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}
