package flaresolverr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"torr/internal/model"
	flareModel "torr/internal/tracker/proxy/flaresolverr/model"
)

const (
	responseOK    = "ok"
	responseError = "error"

	commandGet  = "request.get"
	commandPost = "request.post"
)

type Config struct {
	Host       string
	MaxTimeout int
}

type IClient interface {
	Send(r *http.Request, opts *model.ProxyRequestOptions) (resp *http.Response, err error)
}

type client struct {
	hc   *http.Client
	conf *Config
}

func (f *client) Send(r *http.Request, opts *model.ProxyRequestOptions) (result *http.Response, err error) {
	var (
		data   []byte
		p      *flareModel.Payload
		flresp *flareModel.Response
		resp   *http.Response
	)

	if p, err = f.prepareParams(r, opts); err != nil {
		return
	}
	if data, err = json.Marshal(p); err != nil {
		return
	}
	if resp, err = f.hc.Post(f.conf.Host, "application/json", bytes.NewBuffer(data)); err != nil {
		return
	}
	if err = json.NewDecoder(resp.Body).Decode(&flresp); err != nil {
		return
	}
	result, err = makeResponse(flresp, r)

	return
}

func (f *client) prepareParams(r *http.Request, opts *model.ProxyRequestOptions) (params *flareModel.Payload, err error) {
	var (
		b []byte
	)
	p := &flareModel.Payload{
		URL:         r.URL.String(),
		MaxTimeout:  f.conf.MaxTimeout,
		Session:     opts.Session,
		OnlyCookies: !opts.WithBody,
		Cookies:     opts.Cookies,
	}
	switch r.Method {
	case http.MethodGet:
		p.Cmd = commandGet
	case http.MethodPost:
		if b, err = io.ReadAll(r.Body); err != nil {
			err = fmt.Errorf("error reading proxy request body")
			return
		}
		p.PostData = string(b)
		p.Cmd = commandPost
	default:
		err = fmt.Errorf("unknown http method: %s", r.Method)
		return
	}
	params = p

	return
}

func makeResponse(flresp *flareModel.Response, req *http.Request) (result *http.Response, err error) {
	var (
		cv *http.Cookie
	)

	if flresp.Status == responseError {
		err = fmt.Errorf("flaresolverr error: %s", flresp.Message)
		return
	}
	result = &http.Response{
		StatusCode: flresp.Solution.Status,
		Body:       io.NopCloser(bytes.NewBufferString(flresp.Solution.Response)),
		Request:    req,
		Header: http.Header{
			"Content-Type":   []string{"text/html; charset=Windows-1251"}, // TODO: add parser for content type
			"Location":       []string{flresp.Solution.Url},
			"Content-Length": []string{fmt.Sprint(len(flresp.Solution.Response))},
			"User-Agent":     []string{flresp.Solution.UserAgent},
		},
	}
	for _, cv = range flresp.Solution.Cookies {
		result.Header.Add("Set-Cookie", cv.String())
	}

	return
}

func New(hc *http.Client, conf *Config) IClient {
	return &client{
		hc:   hc,
		conf: conf,
	}
}
