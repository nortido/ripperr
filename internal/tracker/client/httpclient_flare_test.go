package client

import (
	"net/http"
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
