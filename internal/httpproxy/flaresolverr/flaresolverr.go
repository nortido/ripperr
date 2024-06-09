package flaresolverr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type ServiceParams struct {
	Host       string
	MaxTimeout int
}

type Client interface {
	Get(r *http.Request) (resp *http.Response, err error)
}

type httpParams struct {
	Cmd        string `json:"cmd"`
	URL        string `json:"url"`
	Cookies    string `json:"cookies,omitempty"`
	MaxTimeout int    `json:"maxTimeout"`
	PostData   string `json:"postData,omitempty"`
}

type client struct {
	hc   *http.Client
	conf *ServiceParams
}

func (f *client) Get(r *http.Request) (resp *http.Response, err error) {
	var (
		p    *httpParams
		data []byte
	)

	if p, err = f.getParams(r); err != nil {
		return
	}
	data, err = json.Marshal(p)

	return f.hc.Post(f.conf.Host, "application/json", bytes.NewBuffer(data))
}

func (f *client) getParams(r *http.Request) (params *httpParams, err error) {
	p := &httpParams{
		URL:        r.URL.String(),
		MaxTimeout: f.conf.MaxTimeout,
	}
	switch r.Method {
	case http.MethodGet:
		p.Cmd = "request.get"
	case http.MethodPost:
		p.Cmd = "request.post"
		p.PostData = r.PostForm.Encode()
	default:
		err = fmt.Errorf("unknown http method: %s", r.Method)
		return
	}
	for _, c := range r.Cookies() {
		if c != nil {
			p.Cookies += c.String() + "; "
		}
	}
	return
}

func New(hc *http.Client, conf *ServiceParams) Client {
	return &client{
		hc:   hc,
		conf: conf,
	}
}
