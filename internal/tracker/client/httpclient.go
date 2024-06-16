package client

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"torr/internal/helper"
	"torr/internal/model/proxy"
)

type ITracker interface {
	Name() (name string)
	Detect(url string) bool
	Header() (header *http.Header)
	Host() (host string)
	DownloadLink(topicID string) (url string)
	TopicLink(topicID string) (url string)
	LoginData() (link string, data *strings.Reader, cookies []*http.Cookie)
	Datetime(b *bytes.Buffer, encoded bool) (result *time.Time)
	ProxyType() (proxyType proxy.ProxyType)
}

type proxyService interface {
	Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (resp *http.Response, err error)
}

type IClient interface {
	ITracker
	InitSession(ctx context.Context) (err error)
	GetResponse(ctx context.Context, link string) (res *http.Response, err error)
}

type client struct {
	ITracker
	session string
	hc      *http.Client
	header  *http.Header
	cookies []*http.Cookie
	px      proxyService
	cb      *helper.CircuitBreaker
}

func (c *client) InitSession(ctx context.Context) (err error) {
	var (
		link string
		body *strings.Reader
		req  *http.Request
		res  *http.Response
	)
	link, body, c.cookies = c.ITracker.LoginData()

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
	if c.cookies != nil {
		c.header = c.ITracker.Header()
		c.hc.Jar.SetCookies(req.URL, c.cookies)
		return
	}
	res, err = c.tryProxy(ctx, req, &proxy.Options{OnlyCookies: body != nil}, c.ITracker.ProxyType())

	if err != nil || res.StatusCode >= http.StatusBadRequest {
		err = fmt.Errorf("%s login error: %w", c.ITracker.Name(), err)
		return
	}
	c.header = c.ITracker.Header()
	if res.Header.Get("User-Agent") != "" {
		c.header.Set("User-Agent", res.Header.Get("User-Agent"))
	}
	c.hc.Jar.SetCookies(res.Request.URL, res.Cookies())

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
		c.hc.Jar.SetCookies(res.Request.URL, res.Cookies())
		return
	})
	return
}

func (c *client) tryProxy(ctx context.Context, req *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (res *http.Response, err error) {
	if _, ok := c.px.(proxyService); ok {
		res, err = c.px.Send(req, opts, proxyType)
		return
	}
	res, err = c.hc.Do(req)
	return
}

func New(hc *http.Client, t ITracker, px proxyService, cb *helper.CircuitBreaker) IClient {
	return &client{
		hc:       hc,
		ITracker: t,
		px:       px,
		cb:       cb,
	}
}
