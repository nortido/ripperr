package client

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"trackerr/internal/helper"
	"trackerr/internal/model/proxy"
	"trackerr/internal/model/tracker"
)

type ITracker interface {
	Name() (name string)
	Detect(url string) bool
	Header() (header *http.Header)
	Host() (host string)
	DownloadLink(topicID string) (url string)
	LoginData() (link string, credentials *url.Values, cookies []*http.Cookie)
	ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time)
	ProxyType() (proxyType proxy.ProxyType)
}

type proxyService interface {
	Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (resp *http.Response, err error)
}

type IClient interface {
	ITracker
	Cookies() (res []*http.Cookie)
	InitClient(ctx context.Context, payload *tracker.Tracker) (err error)
	GetResponse(ctx context.Context, link string, download bool) (res *http.Response, err error)
}

type client struct {
	ITracker
	hc      *http.Client
	px      proxyService
	cb      *helper.CircuitBreaker
	header  *http.Header
	cookies []*http.Cookie
}

func (c *client) InitClient(ctx context.Context, payload *tracker.Tracker) (err error) {
	var (
		req   *http.Request
		res   *http.Response
		creds string
	)

	if payload != nil && payload.Exists {
		c.cookies = payload.Cookies
		creds = payload.Credentials
	}
	c.header = c.ITracker.Header()
	c.setDefaultHeaders()
	if req, err = c.buildLoginRequest(ctx, creds); err != nil {
		return
	}
	// TODO: make public/private param
	if len(c.cookies) == 0 {
		if res, err = c.loginFromPage(ctx, req); err != nil {
			return
		}
		c.cookies = res.Cookies()
	}
	c.hc.Jar.SetCookies(req.URL, c.cookies)

	return
}

func (c *client) Cookies() (res []*http.Cookie) {
	res = c.cookies
	return

}

func (c *client) GetResponse(ctx context.Context, link string, download bool) (res *http.Response, err error) {
	var (
		req *http.Request
		pt  proxy.ProxyType
	)

	// TODO: tracker rate limit
	if req, err = http.NewRequestWithContext(ctx, http.MethodGet, link, nil); err != nil {
		return
	}
	req.Header = *c.header

	if c.header == nil {
		err = fmt.Errorf("no header")
		return
	}
	pt = proxy.TypeNone

	if !download {
		pt = c.ITracker.ProxyType()
	}
	if res, err = c.tryHttp(ctx, req, &proxy.Options{Cookies: c.cookies}, pt); err != nil {
		return
	}
	c.hc.Jar.SetCookies(res.Request.URL, res.Cookies())
	c.cookies = c.hc.Jar.Cookies(res.Request.URL)
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

func (c *client) loginFromPage(ctx context.Context, req *http.Request) (res *http.Response, err error) {
	res, err = c.tryHttp(ctx, req, &proxy.Options{OnlyCookies: true}, c.ITracker.ProxyType())

	if err != nil || res.StatusCode >= http.StatusBadRequest {
		err = fmt.Errorf("%s login error: %w", c.ITracker.Name(), err)
		return
	}
	if res.Header.Get("User-Agent") != "" {
		c.header.Set("User-Agent", res.Header.Get("User-Agent"))
	}

	return
}

func (c *client) buildLoginRequest(ctx context.Context, body string) (req *http.Request, err error) {
	var (
		link    string
		creds   *url.Values
		cookies []*http.Cookie
	)
	link, creds, cookies = c.ITracker.LoginData()

	if len(cookies) > 0 {
		c.cookies = cookies
	}
	if body == "" && creds != nil {
		body = creds.Encode()
	}
	if body == "" {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
		return
	}
	if req, err = http.NewRequestWithContext(ctx, http.MethodPost, link, strings.NewReader(body)); err != nil {
		return
	}
	req.Header = *c.header
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	req.Header.Set("Host", c.ITracker.Host())

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
		if res.StatusCode >= http.StatusBadRequest {
			err = fmt.Errorf("unexpected status code: %d", res.StatusCode)
			return
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
