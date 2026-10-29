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
	SessionTTL  int     `json:"session_ttl_minutes,omitempty"`
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
		fr   *http.Request
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
	if fr, err = http.NewRequestWithContext(r.Context(), http.MethodPost, f.conf.Host, bytes.NewReader(data)); err != nil {
		return
	}
	fr.Header.Set("Content-Type", "application/json")
	if resp, err = f.hc.Do(fr); err != nil {
		return
	}
	// тело дочитываем до конца, иначе соединение не вернется в пул
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
