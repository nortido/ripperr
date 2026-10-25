package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"ripperr/internal/helper"
	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/model/tracker"
	"ripperr/internal/proxy"
	"ripperr/internal/system/logger"
)

const (
	MaxPageResponse     = 8 << 20  // 8 MiB, HTML страницы трекера
	MaxDownloadResponse = 64 << 20 // 64 MiB, файлы .torrent
	maxErrorResponse    = 8 << 10  // 8 KiB, кусок тела при ошибке
)

var ErrTrackerUnavailable = errors.New("tracker unavailable")

func ReadLimited(dst *bytes.Buffer, r io.Reader, limit int64) error {
	n, err := io.Copy(dst, io.LimitReader(r, limit+1))
	if n > limit {
		return fmt.Errorf("response too large (limit %d)", limit)
	}
	return err
}

type ITracker = tracker.ITracker

type IClient interface {
	ITracker
	Cookies() (res []*http.Cookie)
	InitClient(ctx context.Context, payload *tracker.Tracker, forceLogin bool) (err error)
	GetResponse(ctx context.Context, link string, download bool) (res *http.Response, err error)
}

type Option func(*client)

func WithFlareSession(id string) Option { return func(c *client) { c.flareSession = id } }

type client struct {
	ITracker
	hc         *http.Client
	downloadHC helper.HTTPDoer
	px         proxy.IClient
	cb         *helper.CircuitBreaker
	header     *http.Header
	cm         sync.RWMutex
	cookies    []*http.Cookie
	// своя сессия вместо имени трекера: проверка в настройках не трогает боевую
	flareSession string
}

type cookieEnhancer interface {
	EnhanceCookies(cookies []*http.Cookie)
}

type FlareSessionProvider interface {
	FlareSession() string
}

type postLoginFetcher interface {
	PostLoginURL() string
}

type cookieCompletenessChecker interface {
	CookiesIncomplete(cookies []*http.Cookie) bool
}

type loginAuthValidator interface {
	// только у трекеров, которым после входа через Flare нужны куки авторизации
	// (uid/pass у kinozal). Сессионным вроде rutracker не нужен
	LoginAuthIncomplete(cookies []*http.Cookie) bool
	LoginAuthHint() string
}

func (c *client) flareSessionID() (string, bool) {
	p, ok := c.ITracker.(FlareSessionProvider)
	if !ok {
		return "", false
	}
	// обертка зеркала реализует метод всегда, пустое имя значит "сессии нет"
	id := p.FlareSession()
	if id != "" && c.flareSession != "" {
		id = c.flareSession
	}
	return id, id != ""
}

func (c *client) finalizeCookies(cookies []*http.Cookie) []*http.Cookie {
	if len(cookies) == 0 {
		return cookies
	}
	helper.EnrichCookies(cookies, c.GetDomain(), time.Hour)
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
		c.setCookies(helper.CloneCookies(payload.Cookies))
		creds = payload.Credentials
		savedCookies = helper.CloneCookies(payload.Cookies)
	}
	if forceLogin {
		c.resetFlareSession(ctx)
		c.setCookies(nil)
	}
	if !forceLogin && len(c.cookieSnapshot()) > 0 {
		if validator, ok := c.ITracker.(loginAuthValidator); ok && validator.LoginAuthIncomplete(c.cookieSnapshot()) {
			c.setCookies(nil)
		} else if checker, ok := c.ITracker.(cookieCompletenessChecker); ok && checker.CookiesIncomplete(c.cookieSnapshot()) {
			c.setCookies(nil)
		}
	}
	c.header = c.Headers()
	c.setDefaultHeaders()

	if c.GetPublicity() == tracker.PublicityPublic || payload.Publicity == tracker.PublicityPublic {
		return
	}
	if req, err = c.buildLoginRequest(ctx, creds); err != nil {
		return
	}
	if len(c.cookieSnapshot()) == 0 {
		if !forceLogin {
			c.resetFlareSession(ctx)
		}
		if err = c.preflightFlareLogin(ctx); err != nil {
			return
		}
		if res, err = c.loginFromPage(ctx, req); err != nil {
			if !forceLogin && len(savedCookies) > 0 {
				logger.Warn(fmt.Sprintf("%s login failed, using cookies from db: %s", c.GetName(), err))
				c.setCookies(savedCookies)
				c.setCookies(c.finalizeCookies(c.cookieSnapshot()))
				c.hc.Jar.SetCookies(req.URL, c.cookieSnapshot())
				return c.validateLoginAuth()
			}
			return
		}
		c.setCookies(helper.MergeCookies(c.cookieSnapshot(), res.Cookies()))
		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		c.syncFlarePageCookies(ctx)
	}
	c.setCookies(c.finalizeCookies(c.cookieSnapshot()))
	if err = c.validateLoginAuth(); err != nil {
		return
	}
	c.hc.Jar.SetCookies(req.URL, c.cookieSnapshot())
	return
}

func (c *client) validateLoginAuth() error {
	v, ok := c.ITracker.(loginAuthValidator)
	if !ok || !v.LoginAuthIncomplete(c.cookieSnapshot()) {
		return nil
	}
	msg := "login failed: no auth cookies (uid/pass)"
	if hint := v.LoginAuthHint(); hint != "" {
		msg += ": " + hint
	}
	return fmt.Errorf("%s: %s", c.GetName(), msg)
}

func (c *client) cookieSnapshot() []*http.Cookie {
	c.cm.RLock()
	defer c.cm.RUnlock()
	return helper.CloneCookies(c.cookies)
}

func (c *client) setCookies(v []*http.Cookie) {
	c.cm.Lock()
	c.cookies = v
	c.cm.Unlock()
}

func (c *client) mergeCookies(extra []*http.Cookie) {
	// слияние под одной блокировкой: параллельный запрос мог добавить куку, пока шла сеть
	c.cm.Lock()
	c.cookies = c.finalizeCookies(helper.MergeCookies(c.cookies, extra))
	c.cm.Unlock()
}

func (c *client) Cookies() (res []*http.Cookie) {
	res = c.cookieSnapshot()
	return

}

func (c *client) GetResponse(ctx context.Context, link string, download bool) (res *http.Response, err error) {
	var req *http.Request

	if c.header == nil {
		err = fmt.Errorf("no header")
		return
	}
	if !download {
		link = c.PageLink(link)
	}
	link = c.pageRequestURL(link)
	if download {
		if err = c.ensureFlareCookiesForDownload(ctx, link); err != nil {
			return
		}
	}
	if req, err = http.NewRequestWithContext(ctx, http.MethodGet, link, nil); err != nil {
		return
	}
	if download {
		c.applyDownloadHeaders(req)
	} else {
		req.Header = c.header.Clone()
	}
	if res, err = c.tryHttp(ctx, req, c.flareOptions(c.cookieSnapshot(), false), download); err != nil {
		return
	}
	return
}

func (c *client) pageRequestURL(link string) string {
	if link == "" || !c.Detect(link) {
		return link
	}
	active := c.GetDomain()
	if active == "" {
		return link
	}
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	// схема из старой ссылки торрента (http) не должна понижать запрос к трекеру
	u.Scheme = "https"
	if h, err := url.Parse(c.GetHost()); err == nil && h.Scheme != "" {
		u.Scheme = h.Scheme
	}
	u.Host = active
	return u.String()
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
}

func (c *client) flarePageURL() string {
	if fetcher, ok := c.ITracker.(postLoginFetcher); ok {
		if pageURL := fetcher.PostLoginURL(); pageURL != "" {
			return pageURL
		}
	}
	return c.GetHost()
}

func (c *client) syncFlarePageCookies(ctx context.Context) {
	if c.GetProxyType() != proxyModel.TypeFlare {
		return
	}
	if _, ok := c.flareSessionID(); !ok {
		if f, ok := c.ITracker.(postLoginFetcher); !ok || f.PostLoginURL() == "" {
			return
		}
	}
	extra, err := c.fetchPageCookies(ctx, c.flarePageURL())
	if err != nil {
		logger.Warn(fmt.Sprintf("%s flare cookie sync failed: %s", c.GetName(), err))
		return
	}
	c.mergeCookies(extra)
}

func (c *client) preflightFlareLogin(ctx context.Context) (err error) {
	if c.GetProxyType() != proxyModel.TypeFlare || len(c.cookieSnapshot()) > 0 {
		return nil
	}
	if _, ok := c.flareSessionID(); ok {
		return nil
	}
	extra, err := c.fetchPageCookies(ctx, c.GetHost()+"/")
	if err != nil {
		return fmt.Errorf("%s login preflight: %w", c.GetName(), err)
	}
	c.mergeCookies(extra)
	return nil
}

func (c *client) ensureFlareCookiesForDownload(ctx context.Context, link string) (err error) {
	c.syncFlarePageCookies(ctx)
	c.mergeCookies(nil)
	var u *url.URL
	if u, err = url.Parse(link); err != nil {
		return
	}
	c.hc.Jar.SetCookies(u, c.cookieSnapshot())
	return
}

func (c *client) applyDownloadHeaders(req *http.Request) {
	src := c.header
	if src == nil {
		return
	}
	for _, key := range []string{
		"User-Agent",
		"Accept-Language",
		"sec-ch-ua",
		"sec-ch-ua-mobile",
		"sec-ch-ua-platform",
	} {
		if v := src.Get(key); v != "" {
			req.Header.Set(key, v)
		}
	}
	if len(c.cookieSnapshot()) > 0 {
		req.Header.Set("Cookie", helper.CookiesString(c.cookieSnapshot()))
	}
	req.Header.Set("Accept", "application/x-bittorrent,application/octet-stream,*/*;q=0.9")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if topicID := req.URL.Query().Get("t"); topicID != "" {
		if link := c.TopicLink(topicID); link != "" {
			req.Header.Set("Referer", link)
		}
	}
}

func (c *client) flareOptions(cookies []*http.Cookie, onlyCookies bool) *proxyModel.Options {
	opts := &proxyModel.Options{
		OnlyCookies: onlyCookies,
	}
	if len(cookies) > 0 {
		opts.Cookies = cookies
	}
	if sessionID, ok := c.flareSessionID(); ok && sessionID != "" {
		opts.Session = sessionID
	}
	return opts
}

func (c *client) resetFlareSession(ctx context.Context) {
	sessionID, ok := c.flareSessionID()
	if !ok {
		return
	}
	if err := c.px.DestroyFlareSession(ctx, sessionID); err != nil {
		logger.Warn(fmt.Sprintf("%s: flare session reset: %s", c.GetName(), err))
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
	req.Header = c.header.Clone()
	if res, err = c.doHttp(ctx, req, c.flareOptions(c.cookieSnapshot(), true), false); err != nil {
		return
	}
	defer func() { _ = res.Body.Close() }()
	_, _ = io.Copy(io.Discard, res.Body)
	cookies = res.Cookies()
	c.syncUserAgent(res)
	return
}

func (c *client) loginFromPage(ctx context.Context, req *http.Request) (res *http.Response, err error) {
	onlyCookies := c.useFlareSessionLogin()
	res, err = c.doHttp(ctx, req, c.flareOptions(c.cookieSnapshot(), onlyCookies), false)

	if err != nil {
		err = fmt.Errorf("%s login error: %w", c.GetName(), err)
		return
	}
	c.syncUserAgent(res)

	return
}

func (c *client) syncUserAgent(res *http.Response) {
	if res != nil && res.Header.Get("User-Agent") != "" {
		c.header.Set("User-Agent", res.Header.Get("User-Agent"))
	}
}

func (c *client) buildLoginRequest(ctx context.Context, body string) (req *http.Request, err error) {
	var (
		link  = c.LoginLink()
		creds = c.GetCredentials()
	)
	if body == "" && creds != "" {
		body = creds
	}
	if link == "" {
		link = c.GetHost()
	}
	switch body {
	case "":
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	default:
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, link, strings.NewReader(body))
	}
	if err != nil {
		return
	}
	req.Header = c.header.Clone()
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	req.Header.Set("Host", c.GetDomain())

	return
}

func (c *client) tryHttp(ctx context.Context, req *http.Request, opts *proxyModel.Options, download bool) (res *http.Response, err error) {
	err = c.cb.Call(func() (err error) {
		res, err = c.doHttp(ctx, req, opts, download)
		var se *StatusError
		if NotFoundAnswer(ctx) && errors.As(err, &se) && se.Code == http.StatusNotFound {
			// 404 тут значит, что у трекера такого нет. Брейкер его не считает и счетчик не сбрасывает
			err = &helper.NeutralError{Err: err}
		}
		return
	})
	if err != nil {
		err = fmt.Errorf("%s error: %w", c.GetName(), err)
	}
	return
}

func (c *client) doHttp(ctx context.Context, req *http.Request, opts *proxyModel.Options, download bool) (res *http.Response, err error) {
	var doer helper.HTTPDoer = c.hc
	if download && c.downloadHC != nil {
		doer = c.downloadHC
	}
	proxyType := c.GetProxyType()
	if download {
		proxyType = proxyModel.TypeNone
	}
	switch proxyType {
	case proxyModel.TypeNone:
		res, err = doer.Do(req)
	case proxyModel.TypeFlare:
		res, err = c.sendViaFlare(ctx, req, opts, proxyType)
	default:
		err = fmt.Errorf("proxy is not set: %d", proxyType)
	}
	if err == nil {
		err = c.checkResponse(req, res, download)
	}
	return
}

func (c *client) sendViaFlare(ctx context.Context, req *http.Request, opts *proxyModel.Options, proxyType proxyModel.ProxyType) (res *http.Response, err error) {
	// частый разовый сбой FlareSolverr: сессия Selenium застряла на чужой странице, и кука
	// без домена падает в add_cookie с invalid cookie domain. Сброс сессии и один повтор
	// дают чистый браузер
	res, err = c.px.Send(req, opts, proxyType)
	if err == nil {
		c.absorbFlareCookies(res)
		return
	}
	if !isFlareChallengeSolveFailure(err) {
		return
	}
	sessionID, hasSession := c.flareSessionID()
	if !hasSession || sessionID == "" {
		// сессии не было вовсе, повторять с чистого листа нечего
		return
	}
	if ctx.Err() != nil {
		return
	}
	if req.Body != nil {
		if req.GetBody == nil {
			// тело не перечитать. Отдаем исходную ошибку, на повторе ушло бы пустое тело
			return
		}
		var body io.ReadCloser
		if body, err = req.GetBody(); err != nil {
			return
		}
		req.Body = body
	}
	logger.Warn(fmt.Sprintf("%s: flare challenge solve failed, resetting session and retrying request once: %s", c.GetName(), err))
	c.resetFlareSession(ctx)
	res, err = c.px.Send(req, opts, proxyType)
	if err != nil {
		return
	}
	c.absorbFlareCookies(res)
	return
}

func isFlareChallengeSolveFailure(err error) bool {
	// helper.IsFlareTimeout не годится, он ловит и таймауты. У них своя логика в
	// shouldReloginAfterFlare, а повтор со сбросом сессии удвоил бы цену каждого
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Error solving the challenge")
}

type StatusError struct {
	Code int
	Msg  string
}

func (e *StatusError) Error() string { return e.Msg }

type notFoundAnswerKey struct{}

func WithNotFoundAnswer(ctx context.Context) context.Context {
	return context.WithValue(ctx, notFoundAnswerKey{}, true)
}

func NotFoundAnswer(ctx context.Context) bool {
	return ctx.Value(notFoundAnswerKey{}) != nil
}

func (c *client) checkResponse(req *http.Request, res *http.Response, download bool) (err error) {
	// тело закрываем на любой ошибке: вызывающий при err ответ не получает
	defer func() {
		if err != nil && res.Body != nil {
			_ = res.Body.Close()
		}
	}()
	if !download && res.Request != nil && res.Request.URL.String() != req.URL.String() {
		err = fmt.Errorf("unexpected response url: %s", res.Request.URL.String())
		return
	}
	if download && res.StatusCode >= http.StatusMultipleChoices && res.StatusCode < http.StatusBadRequest {
		err = fmt.Errorf("download redirect not followed (status %d)", res.StatusCode)
		return
	}
	if res.StatusCode >= http.StatusBadRequest {
		b := new(bytes.Buffer)
		if err = ReadLimited(b, res.Body, maxErrorResponse); err != nil {
			return
		}
		contentType := res.Header.Get("Content-Type")
		bodySize := b.Len()
		logger.Debug(fmt.Sprintf("%s: unexpected status %d (type=%s, bytes=%d): %s",
			c.GetName(), res.StatusCode, contentType, bodySize, helper.Substr(b.String(), 0, 500)))
		err = &StatusError{Code: res.StatusCode, Msg: fmt.Sprintf("%s: unexpected status code %d (type=%s, bytes=%d)",
			c.GetName(), res.StatusCode, contentType, bodySize)}
		return
	}
	return
}

func (c *client) useFlareSessionLogin() bool {
	_, ok := c.flareSessionID()
	return ok
}

func (c *client) absorbFlareCookies(res *http.Response) {
	if res == nil {
		return
	}
	if fresh := res.Cookies(); len(fresh) > 0 {
		c.mergeCookies(fresh)
	}
}

func New(hc *http.Client, downloadHC helper.HTTPDoer, t ITracker, px proxy.IClient, cb *helper.CircuitBreaker, opts ...Option) IClient {
	c := &client{
		hc:         hc,
		downloadHC: downloadHC,
		ITracker:   t,
		px:         px,
		cb:         cb,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}
