package tracker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/client"
)

const (
	domainProbeTimeout = 5 * time.Second
	probeBodyLimit     = 64 << 10
)

type domainAliveFunc func(ctx context.Context, domain string) bool

type domainProbe func(tr client.ITracker) domainAliveFunc

type hostCheckFunc func(ctx context.Context, host string) (alive bool, location string)

func domainAliveWith(hc *http.Client) domainAliveFunc {
	check := hostCheckWith(hc)
	return func(ctx context.Context, domain string) bool {
		alive, _ := check(ctx, domain)
		return alive
	}
}

func hostCheckWith(hc *http.Client) hostCheckFunc {
	// клиент трекерный: свой пошел бы мимо прокси и похоронил домен, закрытый только
	// для прямого доступа. Банку кук убираем, чтобы Set-Cookie с зеркал не смешивался
	// с залогиненой сессией
	probe := *hc
	probe.Jar = nil
	return func(ctx context.Context, domain string) (bool, string) {
		ctx, cancel := context.WithTimeout(ctx, domainProbeTimeout)
		defer cancel()
		base := "https://" + domain
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil)
		if err != nil {
			return false, ""
		}
		resp, err := probe.Do(req)
		if err != nil {
			return false, ""
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode >= 500 {
			return false, ""
		}
		// 3xx и 4xx считаем живыми: капчу и челлендж (403) отдает рабочий сайт, flare-трекеры их пройдут
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return true, redirectHost(base, resp)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, probeBodyLimit))
		return err == nil && !blankPage(body), ""
	}
}

func redirectHost(base string, resp *http.Response) string {
	loc := resp.Header.Get("Location")
	if loc == "" || resp.StatusCode < 300 || resp.StatusCode > 399 {
		return ""
	}
	b, err := url.Parse(base + "/")
	if err != nil {
		return ""
	}
	u, err := b.Parse(loc)
	if err != nil {
		return ""
	}
	return u.Host
}

func HostChecker(hc *http.Client) func(ctx context.Context, host string, proxyType int) (alive bool, location string) {
	// режим не важен: страницы всех режимов идут через прокси загрузок, напрямую домен
	// может быть закрыт, и рабочее зеркало считалось бы мертвым
	check := hostCheckWith(hc)
	return func(ctx context.Context, host string, _ int) (bool, string) {
		return check(ctx, host)
	}
}

func blankPage(b []byte) bool {
	inTag := false
	for _, c := range b {
		switch {
		case c == '<':
			inTag = true
		case c == '>':
			inTag = false
		case !inTag && !unicode.IsSpace(rune(c)):
			return false
		}
	}
	return true
}

func domainProbeWith(hc *http.Client) domainProbe {
	check := HostChecker(hc)
	return func(tr client.ITracker) domainAliveFunc {
		proxyType := int(tr.GetProxyType())
		return func(ctx context.Context, domain string) bool {
			alive, _ := check(ctx, domain, proxyType)
			return alive
		}
	}
}

func selectActiveDomain(ctx context.Context, primary string, mirrors []string, alive domainAliveFunc) string {
	if len(mirrors) == 0 {
		return primary
	}
	d, _ := answeringDomain(ctx, primary, mirrors, alive)
	return d
}

func answeringDomain(ctx context.Context, primary string, mirrors []string, alive domainAliveFunc) (string, bool) {
	if alive(ctx, primary) {
		return primary, true
	}
	if d := firstAnswering(ctx, mirrors, alive, ""); d != "" {
		return d, true
	}
	return primary, false
}

func firstAnswering(ctx context.Context, hosts []string, alive domainAliveFunc, skip string) string {
	for _, h := range hosts {
		if h == "" || h == skip {
			continue
		}
		if alive(ctx, h) {
			return h
		}
	}
	return ""
}

func guardedDomain(ctx context.Context, tr client.ITracker, alive domainAliveFunc) (domain, paused string) {
	confirmed, unconfirmed := confirmedMirrors(tr)
	if len(unconfirmed) == 0 {
		return selectActiveDomain(ctx, tr.GetDomain(), confirmed, alive), ""
	}
	domain, ok := answeringDomain(ctx, tr.GetDomain(), confirmed, alive)
	if !ok {
		paused = firstAnswering(ctx, unconfirmed, alive, "")
	}
	return domain, paused
}

type mirrorProvider interface {
	GetMirrors() []string
}

type unconfirmedProvider interface {
	GetUnconfirmed() []string
}

type activeDomainProvider interface {
	GetActiveDomain() string
}

func trackerMirrors(tr client.ITracker) []string {
	if p, ok := tr.(mirrorProvider); ok {
		return p.GetMirrors()
	}
	return nil
}

func confirmedMirrors(tr client.ITracker) (confirmed, unconfirmed []string) {
	mirrors := trackerMirrors(tr)
	p, ok := tr.(unconfirmedProvider)
	if !ok || len(p.GetUnconfirmed()) == 0 {
		return mirrors, nil
	}
	pending := p.GetUnconfirmed()
	for _, m := range mirrors {
		if slices.ContainsFunc(pending, func(u string) bool {
			return strings.EqualFold(strings.TrimSpace(u), strings.TrimSpace(m))
		}) {
			unconfirmed = append(unconfirmed, m)
		} else {
			confirmed = append(confirmed, m)
		}
	}
	return confirmed, unconfirmed
}

func trackerActiveDomain(tr client.ITracker) string {
	if p, ok := tr.(activeDomainProvider); ok {
		return strings.TrimSpace(p.GetActiveDomain())
	}
	return ""
}

type domainOverride struct {
	client.ITracker
	domain   string
	host     string
	loginURL string
}

func (d *domainOverride) GetDomain() string { return d.domain }
func (d *domainOverride) GetHost() string   { return d.host }
func (d *domainOverride) LoginLink() string { return d.loginURL }
func (d *domainOverride) DownloadLink(id string) string {
	return trackerModel.ReplaceURLHost(d.ITracker.DownloadLink(id), d.domain)
}

func (d *domainOverride) PrimaryDomain() string { return d.ITracker.GetDomain() }

func (d *domainOverride) LoginAuthIncomplete(cookies []*http.Cookie) bool {
	if v, ok := d.ITracker.(interface {
		LoginAuthIncomplete([]*http.Cookie) bool
	}); ok {
		return v.LoginAuthIncomplete(cookies)
	}
	return false
}

func (d *domainOverride) LoginAuthHint() string {
	if v, ok := d.ITracker.(interface {
		LoginAuthHint() string
	}); ok {
		return v.LoginAuthHint()
	}
	return ""
}

func (d *domainOverride) FlareSession() string {
	if v, ok := d.ITracker.(interface{ FlareSession() string }); ok {
		return v.FlareSession()
	}
	return ""
}

func (d *domainOverride) PostLoginURL() string {
	if v, ok := d.ITracker.(interface{ PostLoginURL() string }); ok {
		return trackerModel.ReplaceURLHost(v.PostLoginURL(), d.domain)
	}
	return ""
}

func (d *domainOverride) CookiesIncomplete(cookies []*http.Cookie) bool {
	if v, ok := d.ITracker.(interface {
		CookiesIncomplete([]*http.Cookie) bool
	}); ok {
		return v.CookiesIncomplete(cookies)
	}
	return false
}

func (d *domainOverride) EnhanceCookies(cookies []*http.Cookie) {
	if v, ok := d.ITracker.(interface{ EnhanceCookies([]*http.Cookie) }); ok {
		v.EnhanceCookies(cookies)
	}
}

func withActiveDomain(tr client.ITracker, domain string) client.ITracker {
	if domain == "" || tr.GetDomain() == domain {
		return tr
	}
	return &domainOverride{
		ITracker: tr,
		domain:   domain,
		host:     "https://" + domain,
		loginURL: trackerModel.ReplaceURLHost(tr.LoginLink(), domain),
	}
}

func (d *domainOverride) ParsePoster(b *bytes.Buffer, encoded bool) string {
	poster := d.ITracker.ParsePoster(b, encoded)
	// kinozal и похожие собирают адрес постера из своего домена, а он мертвый, раз мы на зеркале
	u, err := url.Parse(poster)
	if err != nil || u.Host != d.ITracker.GetDomain() {
		return poster
	}
	return trackerModel.ReplaceURLHost(poster, d.domain)
}
