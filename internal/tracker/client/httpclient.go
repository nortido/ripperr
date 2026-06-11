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
	GetDomain() (host string)
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
	ParseDownloadID(b *bytes.Buffer, encoded bool) (downloadID string)
}

type proxyService interface {
	Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (resp *http.Response, err error)
	DestroyFlareSession(sessionID string) error
}

type IClient interface {
	ITracker
	Cookies() (res []*http.Cookie)
	InitClient(ctx context.Context, payload *tracker.Tracker, forceLogin bool) (err error)
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

type cookieEnhancer interface {
	EnhanceCookies(cookies []*http.Cookie)
}

type flareSessionProvider interface {
	FlareSession() string
}

type postLoginFetcher interface {
	PostLoginURL() string
}

type cookieCompletenessChecker interface {
	CookiesIncomplete(cookies []*http.Cookie) bool
}

func (c *client) finalizeCookies(cookies []*http.Cookie) []*http.Cookie {
	if len(cookies) == 0 {
		return cookies
	}
	helper.EnrichCookies(cookies, c.ITracker.GetDomain(), time.Hour)
	if enhancer, ok := c.ITracker.(cookieEnhancer); ok {
		enhancer.EnhanceCookies(cookies)
	}
	return cookies
}

func (c *client) InitClient(ctx context.Context, payload *tracker.Tracker, forceLogin bool) (err error) {
	var (
		req          *http.Request
		res          *http.Response
		creds        string
		savedCookies []*http.Cookie
	)
	if payload.Exists {
		c.cookies = payload.Cookies
		creds = payload.Credentials
		savedCookies = helper.ParseCookies(helper.CookiesString(payload.Cookies))
	}
	if forceLogin {
		c.resetFlareSession()
		c.cookies = nil
	}
	if !forceLogin && len(c.cookies) > 0 {
		if checker, ok := c.ITracker.(cookieCompletenessChecker); ok && checker.CookiesIncomplete(c.cookies) {
			c.cookies = nil
		}
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
		c.resetFlareSession()
		if res, err = c.loginFromPage(ctx, req); err != nil {
			c.resetFlareSession()
			if len(savedCookies) > 0 {
				logger.Warn(fmt.Sprintf("%s login failed, using cookies from db: %s", c.ITracker.GetName(), err))
				c.cookies = savedCookies
				c.cookies = c.finalizeCookies(c.cookies)
				c.hc.Jar.SetCookies(req.URL, c.cookies)
				return
			}
			return
		}
		c.cookies = res.Cookies()
		if fetcher, ok := c.ITracker.(postLoginFetcher); ok {
			if pageURL := fetcher.PostLoginURL(); pageURL != "" {
				var (
					extra    []*http.Cookie
					fetchErr error
				)
				if extra, fetchErr = c.fetchPageCookies(ctx, pageURL); fetchErr != nil {
					logger.Warn(fmt.Sprintf("%s post-login fetch failed: %s", c.ITracker.GetName(), fetchErr))
				} else {
					c.cookies = helper.MergeCookies(c.cookies, extra)
				}
			}
		}
	}
	c.cookies = c.finalizeCookies(c.cookies)
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
	if res, err = c.tryHttp(ctx, req, c.flareOptions(c.finalizeCookies(c.cookies), false), download); err != nil {
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
	c.header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	c.header.Add("sec-ch-ua", `"Chromium";v="140", "Not=A?Brand";v="24", "Google Chrome";v="140"`)
	c.header.Add("sec-ch-ua-mobile", "?0")
	c.header.Add("sec-ch-ua-platform", `"macOS"`)

	return
}

func (c *client) flareOptions(cookies []*http.Cookie, onlyCookies bool) *proxy.Options {
	opts := &proxy.Options{
		OnlyCookies: onlyCookies,
	}
	if p, ok := c.ITracker.(flareSessionProvider); ok {
		opts.Session = p.FlareSession()
		return opts
	}
	opts.Cookies = cookies
	return opts
}

func (c *client) resetFlareSession() {
	p, ok := c.ITracker.(flareSessionProvider)
	if !ok {
		return
	}
	if err := c.px.DestroyFlareSession(p.FlareSession()); err != nil {
		logger.Warn(fmt.Sprintf("%s: flare session reset: %s", c.ITracker.GetName(), err))
	}
}

func (c *client) fetchPageCookies(ctx context.Context, pageURL string) (cookies []*http.Cookie, err error) {
	var (
		req *http.Request
		res *http.Response
	)
	if req, err = http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil); err != nil {
		return
	}
	req.Header = *c.header
	if res, err = c.doHttp(ctx, req, c.flareOptions(c.cookies, true), false); err != nil {
		return
	}
	cookies = res.Cookies()
	if res.Header.Get("User-Agent") != "" {
		c.header.Set("User-Agent", res.Header.Get("User-Agent"))
	}
	return
}

func (c *client) loginFromPage(ctx context.Context, req *http.Request) (res *http.Response, err error) {
	res, err = c.doHttp(ctx, req, c.flareOptions(c.cookies, true), false)

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
	err = c.cb.Call(func() (err error) {
		res, err = c.doHttp(ctx, req, opts, download)
		return
	})
	if err != nil {
		err = fmt.Errorf("%s error: %w", c.ITracker.GetName(), err)
	}
	return
}

func (c *client) doHttp(ctx context.Context, req *http.Request, opts *proxy.Options, download bool) (res *http.Response, err error) {
	var (
		_, proxyExists = c.px.(proxyService)
		proxyType      = c.ITracker.GetProxyType()
	)

	if download {
		proxyType = proxy.TypeNone
	}
	switch {
	case proxyType == proxy.TypeNone:
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
		err = fmt.Errorf("unexpected status code: %d\n%s", res.StatusCode, b.String()[:100])
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
