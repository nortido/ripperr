package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/helper/tlscapture"
	"ripperr/internal/helper/tlsclient"
	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/system/logger"
)

// Порядок заголовков навигации Chrome. Взят по наблюдениям, без отдельной проверки по захвату
var chromeHeaderOrder = []string{
	"content-length", "pragma", "cache-control", "sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform",
	"origin", "content-type", "upgrade-insecure-requests", "user-agent", "accept", "sec-fetch-site",
	"sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "referer", "accept-encoding", "accept-language",
	"cookie", "priority",
}

type chromeIdentifier interface {
	ChromeIdentity(ctx context.Context) (tlscapture.Identity, error)
}

func mergeHeaderOrder(captured, typical []string) []string {
	out := slices.Clone(captured)
	for i, name := range typical {
		if slices.Contains(out, name) {
			continue
		}
		at := 0
		for j := i - 1; j >= 0; j-- {
			if p := slices.Index(out, typical[j]); p >= 0 {
				at = p + 1
				break
			}
		}
		out = slices.Insert(out, at, name)
	}
	return out
}

func (c *client) effectiveProxyType() proxyModel.ProxyType {
	pt := c.GetProxyType()
	if pt == proxyModel.TypeTLS && c.tls != nil && c.tls.flareOnly() {
		return proxyModel.TypeFlare
	}
	return pt
}

func (c *client) directTLS() bool {
	return c.GetProxyType() == proxyModel.TypeTLS && c.effectiveProxyType() == proxyModel.TypeTLS
}

func (c *client) EncodedBody() bool {
	return c.effectiveProxyType() == proxyModel.TypeFlare
}

func (c *client) applyTLSIdentity(ctx context.Context) error {
	ua := c.header.Get("User-Agent")
	id := tlscapture.Identity{UserAgent: ua, Profile: tlsclient.ChromeProfile(ua)}
	if p, ok := c.px.(chromeIdentifier); ok {
		got, err := p.ChromeIdentity(ctx)
		if err != nil {
			logger.Warn(fmt.Sprintf("%s: chrome identity: %s", c.GetName(), err))
			// рабочую идентичность общей сессии не меняем на запасную из-за сбоя Flare
			if c.tls.isApplied() {
				return nil
			}
		} else {
			id = got
			if id.UserAgent == "" {
				id.UserAgent = ua
			}
			if id.Profile == "" {
				id.Profile = tlsclient.ChromeProfile(id.UserAgent)
			}
		}
	}
	var timeout time.Duration
	if c.hc != nil {
		timeout = c.hc.Timeout
	}
	proxyURL, err := c.pageProxyURL()
	if err != nil {
		return err
	}
	return c.tls.apply(id, *c.header, timeout, proxyURL)
}

func (c *client) pageProxyURL() (string, error) {
	// тот же выход, что у страниц в режиме 0: с IP сервера трекер может быть закрыт
	target, err := url.Parse(c.GetHost())
	if err != nil {
		return "", err
	}
	req := &http.Request{URL: target}
	var u *url.URL
	switch tr := c.hcTransport().(type) {
	case nil:
		u, err = http.ProxyFromEnvironment(req)
	case *http.Transport:
		if tr.Proxy != nil {
			u, err = tr.Proxy(req)
		}
	}
	if err != nil || u == nil {
		return "", err
	}
	return u.String(), nil
}

func (c *client) hcTransport() http.RoundTripper {
	if c.hc == nil {
		return nil
	}
	return c.hc.Transport
}

func (c *client) sendTLSOnce(ctx context.Context, req *http.Request) (*http.Response, error) {
	doer, headers := c.tls.page()
	if doer == nil {
		return nil, errors.New("tls session is not ready")
	}
	out := req.Clone(ctx)
	for name, vals := range headers {
		out.Header[name] = slices.Clone(vals)
	}
	// h2 сам добавит content-length, а явный ключ ушел бы вторым заголовком
	out.Header.Del("Host")
	out.Header.Del("Content-Length")
	out.Header.Del("Cookie")
	var jar http.CookieJar
	if c.hc != nil {
		jar = c.hc.Jar
	}
	if jar != nil {
		if s := helper.CookiesString(jar.Cookies(out.URL)); s != "" {
			out.Header.Set("Cookie", s)
		}
	}
	res, err := doer.Do(out)
	if err != nil {
		return nil, err
	}
	if jar != nil {
		jar.SetCookies(out.URL, res.Cookies())
	}
	return res, nil
}

func (c *client) sendTLS(ctx context.Context, req *http.Request) (*http.Response, error) {
	started := time.Now()
	res, err := c.sendTLSOnce(ctx, req)
	if err != nil {
		return nil, err
	}
	challenged, err := cloudflareChallenge(res)
	if err != nil {
		_ = res.Body.Close()
		return nil, err
	}
	if !challenged || (req.Body != nil && req.GetBody == nil) {
		return res, nil
	}
	_ = res.Body.Close()
	if err = c.solveChallenge(ctx, req, started); err != nil {
		return nil, err
	}
	retry := req.Clone(ctx)
	if req.Body != nil {
		if retry.Body, err = req.GetBody(); err != nil {
			return nil, err
		}
	}
	if !c.directTLS() {
		// после решения идентичность ушла в flare-only, дальше как обычная страница через Flare
		return c.sendViaFlare(ctx, retry, c.flareOptions(c.cookieSnapshot(), false), proxyModel.TypeFlare)
	}
	res, err = c.sendTLSOnce(ctx, retry)
	if err != nil {
		return nil, err
	}
	challenged, err = cloudflareChallenge(res)
	if err != nil {
		_ = res.Body.Close()
		return nil, err
	}
	if !challenged {
		return res, nil
	}
	_ = res.Body.Close()
	profile := c.tls.profile()
	if c.tls.challengeWarned.CompareAndSwap(false, true) {
		logger.Warn(fmt.Sprintf("%s: cloudflare challenge persists after flaresolverr solve (profile %s)", c.GetName(), profile))
	}
	return nil, &StatusError{Code: res.StatusCode, Msg: fmt.Sprintf("%s: cloudflare challenge after flaresolverr solve (profile %s)", c.GetName(), profile)}
}

func (c *client) solveChallenge(ctx context.Context, req *http.Request, started time.Time) error {
	s := c.tls
	s.solveMu.Lock()
	defer s.solveMu.Unlock()
	// решение, которое закончилось после старта запроса, уже положило cf_clearance в jar
	if started.Before(s.solvedAt) {
		return nil
	}
	solve := req.Clone(ctx)
	solve.Method, solve.Body, solve.GetBody, solve.ContentLength = http.MethodGet, nil, nil, 0
	solve.Header.Del("Content-Length")
	// слот Flare занят только внутри Send, ChromeIdentity ниже зовем уже после него
	fres, err := c.sendViaFlare(ctx, solve, c.flareOptions(c.cookieSnapshot(), true), proxyModel.TypeFlare)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%s: flaresolverr solve: %w", c.GetName(), ctxErr)
		}
		return fmt.Errorf("%s: flaresolverr solve: %v", c.GetName(), err)
	}
	_ = fres.Body.Close()
	if c.hc != nil && c.hc.Jar != nil {
		c.hc.Jar.SetCookies(req.URL, c.cookieSnapshot())
	}
	s.solvedAt = time.Now()
	ua := fres.Header.Get("User-Agent")
	logger.Info(fmt.Sprintf("%s: cloudflare challenge solved via flaresolverr", c.GetName()))
	if ua != "" && ua != s.headerSet().Get("User-Agent") {
		return c.applyTLSIdentity(ctx)
	}
	return nil
}
