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
	InitCookies(ctx context.Context) (err error)
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

func (c *client) InitCookies(ctx context.Context) (err error) {
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
	c.header = c.ITracker.Header()
	c.setDefaultHeaders()
	if req.Method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Content-Length", fmt.Sprintf("%d", body.Len()))
		req.Header.Set("Host", c.ITracker.Host())
	}
	if c.cookies != nil {
		c.hc.Jar.SetCookies(req.URL, c.cookies)
		return
	}
	req.Header = *c.header
	res, err = c.tryHttp(ctx, req, &proxy.Options{OnlyCookies: body != nil}, c.ITracker.ProxyType())

	if err != nil || res.StatusCode >= http.StatusBadRequest {
		err = fmt.Errorf("%s login error: %w", c.ITracker.Name(), err)
		return
	}
	if res.Header.Get("User-Agent") != "" {
		c.header.Set("User-Agent", res.Header.Get("User-Agent"))
	}
	c.cookies = res.Cookies()
	c.hc.Jar.SetCookies(res.Request.URL, c.cookies)

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
	if res, err = c.tryHttp(ctx, req, nil, proxy.TypeNone); err != nil {
		return
	}
	if res.StatusCode >= http.StatusBadRequest {
		err = fmt.Errorf("unexpected status code: %d", res.StatusCode)
		return
	}
	c.hc.Jar.SetCookies(res.Request.URL, res.Cookies())
	return
}

func (c *client) setDefaultHeaders() {
	c.header.Add("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	c.header.Add("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	c.header.Add("Cache-Control", "no-cache")
	c.header.Add("Pragma", "no-cache")
	c.header.Add("Priority", "u=0, i")
	c.header.Add("Sec-Fetch-Dest", "document")
	c.header.Add("Sec-Fetch-Mode", "navigate")
	c.header.Add("Sec-Fetch-Site", "same-origin")
	c.header.Add("Sec-Fetch-User", "?1")
	c.header.Add("Upgrade-Insecure-Requests", "1")
	c.header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	c.header.Add("sec-ch-ua", `"Google Chrome";v="125", "Chromium";v="125", "Not.A/Brand";v="24"`)
	c.header.Add("sec-ch-ua-mobile", "?0")
	c.header.Add("sec-ch-ua-platform", `"macOS"`)

	return
}

func (c *client) tryHttp(ctx context.Context, req *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (res *http.Response, err error) {
	err = c.cb.Call(func() (err error) {
		if _, ok := c.px.(proxyService); ok {
			res, err = c.px.Send(req, opts, proxyType)
			return
		}
		if proxyType != proxy.TypeNone {
			return
		}
		if res, err = c.hc.Do(req); err != nil {
			return
		}
		if res.Request != nil && res.Request.URL.String() != req.URL.String() {
			err = fmt.Errorf("unexpected response url: %s", res.Request.URL.String())
		}
		return
	})
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
