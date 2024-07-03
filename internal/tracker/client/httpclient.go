package client

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"trackerr/internal/helper"
	"trackerr/internal/model/proxy"
	"trackerr/internal/model/tracker"
	"trackerr/internal/system/logger"
)

type ITracker interface {
	GetName() (name string)
	GetHost() (host string)
	GetProxyType() (proxyType proxy.ProxyType)
	GetPublicity() (publicity string)
	GetHourLimit() (limit int)
	GetCookies() (cookies []*http.Cookie)
	GetCredentials() (credentials string)
	LoginLink() (url string)
	DownloadLink(topicID string) (url string)
	Detect(url string) bool
	Headers() (header *http.Header)
	ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time)
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
	if payload.Exists {
		c.cookies = payload.Cookies
		creds = payload.Credentials
	}
	c.header = c.ITracker.Headers()
	c.setDefaultHeaders()

	switch tracker.PublicityPublic {
	case c.ITracker.GetPublicity(), payload.Publicity:
		return
	}
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
	var req *http.Request

	if req, err = http.NewRequestWithContext(ctx, http.MethodGet, link, nil); err != nil {
		return
	}
	req.Header = *c.header

	if c.header == nil {
		err = fmt.Errorf("no header")
		return
	}
	if res, err = c.tryHttp(ctx, req, &proxy.Options{Cookies: c.cookies}, download); err != nil {
		return
	}
	//if !download {
	//	c.hc.Jar.SetCookies(res.Request.URL, res.Cookies())
	//	c.cookies = c.hc.Jar.Cookies(res.Request.URL)
	//}
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
	res, err = c.tryHttp(ctx, req, &proxy.Options{OnlyCookies: true}, false)

	if err != nil || res.StatusCode >= http.StatusBadRequest {
		err = fmt.Errorf("%s login error: %w", c.ITracker.GetName(), err)
		return
	}
	if res.Header.Get("User-Agent") != "" {
		c.header.Set("User-Agent", res.Header.Get("User-Agent"))
	}

	return
}

func (c *client) buildLoginRequest(ctx context.Context, body string) (req *http.Request, err error) {
	var (
		link    = c.ITracker.LoginLink()
		creds   = c.ITracker.GetCredentials()
		cookies = c.ITracker.GetCookies()
	)
	if len(cookies) > 0 {
		c.cookies = cookies
	}
	if body == "" && creds != "" {
		body = creds
	}
	if link == "" {
		link = c.ITracker.GetHost()
	}
	switch {
	case body == "":
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	default:
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, link, strings.NewReader(body))
	}
	if err != nil {
		return
	}
	req.Header = *c.header
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	req.Header.Set("Host", c.ITracker.GetHost())

	return
}

func (c *client) tryHttp(ctx context.Context, req *http.Request, opts *proxy.Options, download bool) (res *http.Response, err error) {
	var (
		_, proxyExists = c.px.(proxyService)
		proxyType      = c.ITracker.GetProxyType()
	)

	if download {
		proxyType = proxy.TypeNone
	}
	err = c.cb.Call(func() (err error) {
		switch {
		case proxyType == proxy.TypeNone:
			logger.Info(fmt.Sprintf("getting \"%s\" by direct http", req.URL.String()))
			res, err = c.hc.Do(req)
		case proxyExists:
			res, err = c.px.Send(req, opts, proxyType)
		default:
			err = fmt.Errorf("proxy is not set: %d", proxyType)
		}
		if err == nil {
			err = c.checkResponse(req, res, download)
		}
		return
	})
	if err != nil {
		err = fmt.Errorf("%s error: %w", c.ITracker.GetName(), err)
	}
	return
}

func (c *client) checkResponse(req *http.Request, res *http.Response, download bool) (err error) {
	if res.Request != nil && res.Request.URL.String() != req.URL.String() {
		err = fmt.Errorf("unexpected response url: %s", res.Request.URL.String())
	}
	if res.StatusCode >= http.StatusBadRequest {
		b := new(bytes.Buffer)
		defer res.Body.Close()

		if _, err = b.ReadFrom(res.Body); err != nil {
			return
		}
		err = fmt.Errorf("unexpected status code: %d\n%s", res.StatusCode, b.String()[0:100])
		return
	}
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
