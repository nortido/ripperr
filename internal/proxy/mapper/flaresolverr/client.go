// Package flaresolverr contains a minimal FlareSolverr API client,
// inlined from github.com/nortido/go-flaresolverr to fix a response
// body leak and drop the dependency.
package flaresolverr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	commandGet  = "request.get"
	commandPost = "request.post"
)

type Config struct {
	Host       string
	MaxTimeout int
}

type Client struct {
	hc   *http.Client
	conf *Config
}

type Payload struct {
	Cmd         string  `json:"cmd"`
	URL         string  `json:"url"`
	Cookies     Cookies `json:"cookies,omitempty"`
	OnlyCookies bool    `json:"returnOnlyCookies,omitempty"`
	MaxTimeout  int     `json:"maxTimeout"`
	PostData    string  `json:"postData,omitempty"`
	Session     string  `json:"session,omitempty"`
}

type Response struct {
	Status   string            `json:"status"`
	Message  string            `json:"message"`
	Solution *ResponseSolution `json:"solution"`
}

type ResponseSolution struct {
	Status    int     `json:"status"`
	Response  string  `json:"response"`
	Cookies   Cookies `json:"cookies"`
	UserAgent string  `json:"userAgent"`
}

func (f *Client) Send(r *http.Request, opts *Payload) (result *Response, err error) {
	var (
		b    []byte
		data []byte
		resp *http.Response
	)

	switch r.Method {
	case http.MethodGet:
		opts.Cmd = commandGet
	case http.MethodPost:
		if b, err = io.ReadAll(r.Body); err != nil {
			err = fmt.Errorf("error reading proxy request body")
			return
		}
		opts.PostData = string(b)
		opts.Cmd = commandPost
	default:
		err = fmt.Errorf("unknown http method: %s", r.Method)
		return
	}
	opts.MaxTimeout = f.conf.MaxTimeout
	if data, err = json.Marshal(opts); err != nil {
		return
	}
	if resp, err = f.hc.Post(f.conf.Host, "application/json", bytes.NewBuffer(data)); err != nil {
		return
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return
	}
	return
}

func New(hc *http.Client, conf *Config) *Client {
	return &Client{
		hc:   hc,
		conf: conf,
	}
}
