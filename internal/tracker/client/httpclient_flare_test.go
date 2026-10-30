package client

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"ripperr/internal/helper"
	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/model/tracker"
	"ripperr/internal/tracker/client/kinozal"
)

type flareOptsTracker struct {
	tracker.Tracker
	session string
}

func (t *flareOptsTracker) FlareSession() string { return t.session }

func TestFlareOptionsPassesCookiesWithSession(t *testing.T) {
	c := &client{
		ITracker: &flareOptsTracker{session: "rutracker"},
		cookies:  []*http.Cookie{{Name: "bb_session", Value: "abc"}},
	}
	opts := c.flareOptions(c.cookies, false)
	if opts.Session != "rutracker" {
		t.Fatalf("session = %q", opts.Session)
	}
	if len(opts.Cookies) != 1 || opts.Cookies[0].Name != "bb_session" {
		t.Fatalf("cookies = %+v", opts.Cookies)
	}
}

func TestAbsorbFlareCookies(t *testing.T) {
	tr := &tracker.Tracker{Name: "kinozal", Domain: "kinozal.guru"}
	c := &client{ITracker: tr}
	res := &http.Response{
		Header: http.Header{},
	}
	res.Header.Add("Set-Cookie", "uid=1")
	res.Header.Add("Set-Cookie", "pass=hash")
	c.absorbFlareCookies(res)
	if len(c.cookies) != 2 {
		t.Fatalf("cookies = %+v", c.cookies)
	}
	if helper.CookiesString(c.cookies) != "uid=1; pass=hash" {
		t.Fatalf("cookie string = %q", helper.CookiesString(c.cookies))
	}
}

func TestAbsorbFlareCookiesPrefersIssuedOverEchoed(t *testing.T) {
	tr := &tracker.Tracker{Name: "kinozal", Domain: "kinozal.me"}
	c := &client{ITracker: tr, cookies: []*http.Cookie{{Name: "cf_clearance", Value: "old"}, {Name: "uid", Value: "1"}}}
	res := &http.Response{Header: http.Header{}}
	res.Header.Add("Set-Cookie", "cf_clearance=new; Domain=.kinozal.me")
	res.Header.Add("Set-Cookie", "uid=1")
	res.Header.Add("Set-Cookie", "cf_clearance=old")
	c.absorbFlareCookies(res)
	if got := helper.CookiesString(c.cookies); got != "cf_clearance=new; uid=1" {
		t.Fatalf("cookie string = %q", got)
	}
}

func TestFlareOptionsOmitsEmptyCookiesWithoutSession(t *testing.T) {
	c := &client{ITracker: &tracker.Tracker{ProxyType: proxyModel.TypeFlare}}
	opts := c.flareOptions(nil, false)
	if opts.Cookies != nil {
		t.Fatalf("expected nil cookies, got %+v", opts.Cookies)
	}
}

func TestUseFlareSessionLogin(t *testing.T) {
	withSession := &client{ITracker: &flareOptsTracker{session: "rutracker"}}
	if !withSession.useFlareSessionLogin() {
		t.Fatal("expected session login for FlareSession tracker")
	}
	withoutSession := &client{ITracker: &tracker.Tracker{ProxyType: proxyModel.TypeFlare}}
	if withoutSession.useFlareSessionLogin() {
		t.Fatal("expected no session login for cookie-based flare tracker")
	}
}

type kinozalActiveHost struct {
	*kinozal.Tracker
	active string
}

func (k *kinozalActiveHost) GetDomain() string { return k.active }

func TestPageRequestURLRewritesTrackerHost(t *testing.T) {
	tr := &kinozalActiveHost{
		Tracker: kinozal.New(tracker.Tracker{Domain: "kinozal.tv", Mirrors: []string{"mirror.example"}}),
		active:  "kinozal.guru",
	}
	c := &client{ITracker: tr}
	got := c.pageRequestURL("https://kinozal.tv/details.php?id=2136201")
	want := "https://kinozal.guru/details.php?id=2136201"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := c.pageRequestURL("https://rutracker.org/forum/viewtopic.php?t=1"); got != "https://rutracker.org/forum/viewtopic.php?t=1" {
		t.Fatalf("foreign url rewritten: %q", got)
	}
	if got := c.pageRequestURL("https://kinozal.guru/details.php?id=1"); got != "https://kinozal.guru/details.php?id=1" {
		t.Fatalf("already active: %q", got)
	}
}

type emptyFlareTracker struct {
	tracker.Tracker
}

func (emptyFlareTracker) FlareSession() string { return "" }
func (emptyFlareTracker) PostLoginURL() string { return "" }

type countingProxy struct {
	sends int
}

func (p *countingProxy) Send(r *http.Request, _ *proxyModel.Options, _ proxyModel.ProxyType) (*http.Response, error) {
	p.sends++
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: http.NoBody, Request: r}, nil
}

func (p *countingProxy) DestroyFlareSession(context.Context, string) error { return nil }

func TestEmptyFlareHelpersMeanAbsent(t *testing.T) {
	px := &countingProxy{}
	c := &client{
		ITracker: &emptyFlareTracker{Tracker: tracker.Tracker{Name: "nnm", Domain: "nnm.example", ProxyType: proxyModel.TypeFlare}},
		px:       px,
		header:   &http.Header{},
	}
	c.cookies = []*http.Cookie{{Name: "a", Value: "1"}}
	c.syncFlarePageCookies(t.Context())
	if got := c.cookieSnapshot(); len(got) != 1 || px.sends != 0 {
		t.Fatalf("sync fetched a page: sends=%d cookies=%+v", px.sends, got)
	}
	c.setCookies(nil)
	if err := c.preflightFlareLogin(t.Context()); err != nil || px.sends != 1 {
		t.Fatalf("preflight skipped: sends=%d err=%v", px.sends, err)
	}
}

type httpOnlyHost struct {
	tracker.Tracker
}

func (httpOnlyHost) GetHost() string { return "http://lan.example" }

func TestPageRequestURLUsesTrackerScheme(t *testing.T) {
	c := &client{ITracker: &tracker.Tracker{Domain: "tracker.example", Mirrors: []string{"mirror.example"}}}
	if got := c.pageRequestURL("http://tracker.example/viewtopic.php?t=1"); got != "https://tracker.example/viewtopic.php?t=1" {
		t.Fatalf("got %q", got)
	}
	if got := c.pageRequestURL("http://mirror.example/viewtopic.php?t=1"); got != "https://tracker.example/viewtopic.php?t=1" {
		t.Fatalf("mirror link: got %q", got)
	}
	plain := &client{ITracker: &httpOnlyHost{Tracker: tracker.Tracker{Domain: "lan.example"}}}
	if got := plain.pageRequestURL("http://lan.example/viewtopic.php?t=1"); got != "http://lan.example/viewtopic.php?t=1" {
		t.Fatalf("http tracker downgraded or changed: %q", got)
	}
}

type blockingFlareProxy struct {
	entered chan struct{}
	release chan struct{}
}

func (p *blockingFlareProxy) Send(r *http.Request, _ *proxyModel.Options, _ proxyModel.ProxyType) (*http.Response, error) {
	close(p.entered)
	<-p.release
	h := http.Header{}
	h.Add("Set-Cookie", "cf=2")
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: http.NoBody, Request: r}, nil
}

func (p *blockingFlareProxy) DestroyFlareSession(context.Context, string) error { return nil }

func TestDownloadFlareSyncKeepsConcurrentCookie(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	px := &blockingFlareProxy{entered: make(chan struct{}), release: make(chan struct{})}
	c := &client{
		ITracker: &flareOptsTracker{
			Tracker: tracker.Tracker{Name: "flare", Domain: "flare.example", ProxyType: proxyModel.TypeFlare},
			session: "s",
		},
		hc:     &http.Client{Jar: jar},
		px:     px,
		header: &http.Header{},
	}
	done := make(chan error, 1)
	go func() { done <- c.ensureFlareCookiesForDownload(t.Context(), "https://flare.example/dl?t=1") }()
	<-px.entered
	res := &http.Response{Header: http.Header{}}
	res.Header.Add("Set-Cookie", "fresh=1")
	c.absorbFlareCookies(res)
	close(px.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := helper.CookiesString(c.cookieSnapshot())
	if !strings.Contains(got, "fresh=1") || !strings.Contains(got, "cf=2") {
		t.Fatalf("cookies = %q, want fresh=1 and cf=2", got)
	}
}
