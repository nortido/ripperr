package client

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"torr/internal/helper"
	"torr/internal/model"
)

type ITracker interface {
	Header() (header *http.Header)
	Host() (host string)
	LoginData() (link string, data *strings.Reader)
	DownloadLink(topicID string) (url string)
	TopicLink(topicID string) (url string)
	Datetime(b *bytes.Buffer, encoded bool) (result *time.Time)
	Detect(url string) bool
	Name() (name string)
}

type proxy interface {
	Send(r *http.Request, opts *model.ProxyRequestOptions) (resp *http.Response, err error)
}

type IClient interface {
	ITracker
	InitSession(ctx context.Context) (err error)
	GetResponse(ctx context.Context, link string) (res *http.Response, err error)
}

type client struct {
	ITracker //TODO: make tracker implemented
	session  string
	hc       *http.Client
	header   *http.Header
	cookies  []*http.Cookie
	px       proxy
	cb       *helper.CircuitBreaker
}

func (c *client) InitSession(ctx context.Context) (err error) {
	var (
		link string
		body *strings.Reader
		req  *http.Request
		res  *http.Response
	)
	link, body = c.ITracker.LoginData()

	if body != nil {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, link, body)
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	}
	if err != nil {
		return
	}
	if req.Method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Content-Length", fmt.Sprintf("%d", body.Len()))
		req.Header.Set("Host", c.ITracker.Host())
	}
	res, err = c.tryProxy(ctx, req, &model.ProxyRequestOptions{Session: "", WithBody: body != nil})

	if err != nil || res.StatusCode >= http.StatusBadRequest {
		err = fmt.Errorf("%s login error: %w", c.ITracker.Name(), err)
		return
	}
	// TODO: add extra headers if no flaresolverr
	c.header = c.ITracker.Header()
	c.grabResponseHeaders(res)

	return
}

func (c *client) GetResponse(ctx context.Context, link string) (res *http.Response, err error) {
	var req *http.Request
	if req, err = http.NewRequestWithContext(ctx, http.MethodGet, link, nil); err != nil {
		return
	}
	req.Header = *c.header

	if c.header == nil {
		err = fmt.Errorf("no header")
		return
	}

	err = c.cb.Call(func() (err error) {
		res, err = c.hc.Do(req)
		if err != nil {
			return
		}
		if res.StatusCode != http.StatusOK {
			err = fmt.Errorf("unexpected status code: %d", res.StatusCode)
			return
		}
		if res.Request != nil && res.Request.URL.String() != link {
			err = fmt.Errorf("unexpected response url: %s", res.Request.URL.String())
		}
		c.grabResponseHeaders(res)
		return
	})
	return
}

func (c *client) tryProxy(ctx context.Context, req *http.Request, opts *model.ProxyRequestOptions) (res *http.Response, err error) {
	if _, ok := c.px.(proxy); ok {
		res, err = c.px.Send(req, opts)
		return
	}
	res, err = c.hc.Do(req)
	return
}

func (c *client) grabResponseHeaders(res *http.Response) (header *http.Header) {
	var cv *http.Cookie
	for _, cv = range res.Cookies() {
		c.cookies = append(c.cookies, cv)
		c.header.Add("Cookie", cv.String())
	}
	c.header.Set("User-Agent", res.Header.Get("User-Agent"))
	return
}

func New(hc *http.Client, t ITracker, px proxy) IClient {
	return &client{
		hc:       hc,
		ITracker: t,
		px:       px,
		cb:       helper.NewCircuitBreaker(2, 10*time.Minute),
	}
}
