package tracker

import (
	"bytes"
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"

	proxyModel "ripperr/internal/model/proxy"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/client"
	"ripperr/internal/tracker/client/kinozal"
	"ripperr/internal/tracker/client/rutracker"
)

type stubTracker struct {
	trackerModel.Tracker
}

func TestSelectActiveDomainNoMirrors(t *testing.T) {
	called := false
	alive := func(context.Context, string) bool {
		called = true
		return true
	}
	got := selectActiveDomain(context.Background(), "primary.org", nil, alive)
	if got != "primary.org" || called {
		t.Fatalf("got %q called=%v", got, called)
	}
}

func TestSelectActiveDomainPrimaryAlive(t *testing.T) {
	alive := func(_ context.Context, domain string) bool {
		return domain == "primary.org"
	}
	got := selectActiveDomain(context.Background(), "primary.org", []string{"mirror.org"}, alive)
	if got != "primary.org" {
		t.Fatalf("got %q", got)
	}
}

func TestTrackerActiveDomainOverridesSelection(t *testing.T) {
	tr := &stubTracker{Tracker: trackerModel.Tracker{
		Domain:       "primary.org",
		ActiveDomain: "picked.example",
		Mirrors:      []string{"mirror.org"},
	}}
	active := trackerActiveDomain(tr)
	if active != "picked.example" {
		t.Fatalf("trackerActiveDomain = %q", active)
	}
}

func TestSelectActiveDomainMirrorFallback(t *testing.T) {
	alive := func(_ context.Context, domain string) bool {
		return domain == "mirror.org"
	}
	got := selectActiveDomain(context.Background(), "primary.org", []string{"mirror.org"}, alive)
	if got != "mirror.org" {
		t.Fatalf("got %q", got)
	}
}

func TestSelectActiveDomainAllDead(t *testing.T) {
	alive := func(context.Context, string) bool { return false }
	got := selectActiveDomain(context.Background(), "primary.org", []string{"mirror.org"}, alive)
	if got != "primary.org" {
		t.Fatalf("got %q", got)
	}
}

func TestWithActiveDomainReplacesHosts(t *testing.T) {
	tr := &stubTracker{Tracker: trackerModel.Tracker{
		Name:        "rutracker",
		Domain:      "rutracker.org",
		LoginURL:    "https://rutracker.org/forum/login.php",
		DownloadURL: "https://rutracker.org/forum/dl.php?t=",
	}}
	wrapped := withActiveDomain(tr, "mirror.example")
	if wrapped.GetDomain() != "mirror.example" {
		t.Fatalf("domain = %q", wrapped.GetDomain())
	}
	if wrapped.LoginLink() != "https://mirror.example/forum/login.php" {
		t.Fatalf("login = %q", wrapped.LoginLink())
	}
	if wrapped.DownloadLink("42") != "https://mirror.example/forum/dl.php?t=42" {
		t.Fatalf("download = %q", wrapped.DownloadLink("42"))
	}
	if !wrapped.Detect("https://rutracker.org/forum/viewtopic.php?t=1") {
		t.Fatal("detect should still match primary domain")
	}
}

func TestDomainOverrideForwardsLoginAuth(t *testing.T) {
	tr := kinozal.New(trackerModel.Tracker{Name: "kinozal", Domain: "kinozal.tv"})
	wrapped := withActiveDomain(tr, "kinozal.guru")
	v, ok := wrapped.(interface {
		LoginAuthIncomplete([]*http.Cookie) bool
		LoginAuthHint() string
	})
	if !ok {
		t.Fatal("domainOverride should forward login auth validator")
	}
	if !v.LoginAuthIncomplete(nil) {
		t.Fatal("expected incomplete auth through wrapper")
	}
	if v.LoginAuthHint() == "" {
		t.Fatal("expected hint through wrapper")
	}
}

var _ client.ITracker = (*stubTracker)(nil)

type posterTracker struct {
	client.ITracker
	domain string
	poster string
}

func (p *posterTracker) GetDomain() string                      { return p.domain }
func (p *posterTracker) ParsePoster(*bytes.Buffer, bool) string { return p.poster }

func TestDomainOverrideParsePosterFollowsMirror(t *testing.T) {
	base := &posterTracker{domain: "kinozal.tv", poster: "https://kinozal.tv/i/poster/1.jpg"}
	d := &domainOverride{ITracker: base, domain: "kinozal.guru"}
	if got, want := d.ParsePoster(nil, false), "https://kinozal.guru/i/poster/1.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDomainOverrideParsePosterKeepsForeignHost(t *testing.T) {
	base := &posterTracker{domain: "kinozal.tv", poster: "https://s.kinozal.tv/i/poster/1.jpg"}
	d := &domainOverride{ITracker: base, domain: "kinozal.guru"}
	if got, want := d.ParsePoster(nil, false), "https://s.kinozal.tv/i/poster/1.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDomainAliveWithKeepsCookiesOutOfSessionJar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "mirror", Value: "1", Path: "/"})
		_, _ = w.Write([]byte("<title>mirror</title>"))
	}))
	defer srv.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Jar: jar}
	// проба ходит по https://<domain>, поэтому подменяем транспорт на адрес сервера
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	hc.Transport = &redirectTransport{host: target.Host}

	if !domainAliveWith(hc)(context.Background(), "mirror.example") {
		t.Fatal("want domain alive")
	}
	if got := jar.Cookies(&url.URL{Scheme: "https", Host: "mirror.example"}); len(got) != 0 {
		t.Fatalf("want empty session jar, got %v", got)
	}
}

type redirectTransport struct {
	host string
	hits int
}

func (r *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.hits++
	req = req.Clone(req.Context())
	req.URL.Scheme = "http"
	req.URL.Host = r.host
	return http.DefaultTransport.RoundTrip(req)
}

func TestDomainProbeKeepsFlareTrackerOffTrackerProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<title>mirror</title>"))
	}))
	defer srv.Close()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	rt := &redirectTransport{host: target.Host}
	probe := domainProbeWith(&http.Client{Transport: rt})

	plain := &stubTracker{Tracker: trackerModel.Tracker{ProxyType: proxyModel.TypeNone}}
	if !probe(plain)(context.Background(), "mirror.example") {
		t.Fatal("want domain alive")
	}
	if rt.hits != 1 {
		t.Fatalf("обычный трекер должен пробоваться клиентом трекера, hits=%d", rt.hits)
	}

	// у flare-трекера свой выход в сеть, проба через прокси трекера соврала бы
	flare := &stubTracker{Tracker: trackerModel.Tracker{ProxyType: proxyModel.TypeFlare}}
	_ = probe(flare)(context.Background(), "mirror.invalid")
	if rt.hits != 1 {
		t.Fatalf("проба flare-трекера ушла транспортом трекера, hits=%d", rt.hits)
	}
}

type suffixTracker struct {
	trackerModel.Tracker
}

func (suffixTracker) DownloadLink(id string) string {
	return "https://primary.example/torrent/" + id + "/download/"
}

func TestWithActiveDomainSuffixDownloadLink(t *testing.T) {
	tr := &suffixTracker{Tracker: trackerModel.Tracker{Domain: "primary.example"}}
	wrapped := withActiveDomain(tr, "mirror.example")
	if got := wrapped.DownloadLink("abc"); got != "https://mirror.example/torrent/abc/download/" {
		t.Fatalf("download = %q", got)
	}
}

func probeStatus(t *testing.T, status int, body string) bool {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status == http.StatusFound {
			w.Header().Set("Location", "https://elsewhere.example/")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{
		Transport:     &redirectTransport{host: target.Host},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return domainAliveWith(hc)(context.Background(), "mirror.example")
}

func TestDomainAliveRejectsBlankOK(t *testing.T) {
	bodies := []string{"", " \n\t", "<html><body> </body></html>"}
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		for _, body := range bodies {
			if probeStatus(t, status, body) {
				t.Errorf("status %d body %q: want dead", status, body)
			}
		}
	}
}

func TestDomainAliveRejectsServerError(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, 522} {
		if probeStatus(t, status, "<title>error</title>") {
			t.Errorf("status %d: want dead", status)
		}
	}
}

func TestDomainAliveKeepsLivePages(t *testing.T) {
	cases := []struct {
		status int
		body   string
	}{
		{http.StatusOK, "<title>RuTracker</title>"},
		{http.StatusForbidden, "Just a moment..."},
		{http.StatusFound, ""},
	}
	for _, c := range cases {
		if !probeStatus(t, c.status, c.body) {
			t.Errorf("status %d body %q: want alive", c.status, c.body)
		}
	}
}

func TestBlankPage(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", true},
		{" \r\n\t", true},
		{"<html><body> </body></html>", true},
		{"<!-- x --> <br/>", true},
		{"ok", false},
		{"<p>text</p>", false},
		{"<a", true},
		{"a < b", false},
	}
	for _, c := range cases {
		if got := blankPage([]byte(c.in)); got != c.want {
			t.Errorf("blankPage(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDomainOverrideForwardsFlareHelpers(t *testing.T) {
	rt := withActiveDomain(rutracker.New(trackerModel.Tracker{Name: "rutracker", Domain: "rutracker.org"}), "rutracker.net")
	sess, ok := rt.(interface{ FlareSession() string })
	if !ok || sess.FlareSession() != "rutracker" {
		t.Fatalf("flare session through wrapper: ok=%v", ok)
	}
	pl, ok := rt.(interface{ PostLoginURL() string })
	if !ok || pl.PostLoginURL() != "https://rutracker.net/forum/index.php" {
		t.Fatalf("post login url through wrapper: ok=%v", ok)
	}
	cc, ok := rt.(interface{ CookiesIncomplete([]*http.Cookie) bool })
	if !ok || !cc.CookiesIncomplete(nil) {
		t.Fatalf("cookies incomplete through wrapper: ok=%v", ok)
	}
	en, ok := rt.(interface{ EnhanceCookies([]*http.Cookie) })
	if !ok {
		t.Fatal("enhance cookies not forwarded")
	}
	cookies := []*http.Cookie{{Name: "bb_session", Value: "x"}}
	en.EnhanceCookies(cookies)
	if cookies[0].Path != "/forum/" || !cookies[0].Secure {
		t.Fatalf("bb_session not enhanced: %+v", cookies[0])
	}

	kz := withActiveDomain(kinozal.New(trackerModel.Tracker{Name: "kinozal", Domain: "kinozal.tv"}), "kinozal.guru")
	if got := kz.(interface{ PostLoginURL() string }).PostLoginURL(); got != "https://kinozal.guru/browse.php" {
		t.Fatalf("kinozal post login url = %q", got)
	}
}

func TestDomainOverrideFlareHelpersEmptyForPlainTracker(t *testing.T) {
	w := withActiveDomain(&stubTracker{Tracker: trackerModel.Tracker{Name: "nnm", Domain: "nnm-club.me"}}, "mirror.example")
	if got := w.(interface{ FlareSession() string }).FlareSession(); got != "" {
		t.Fatalf("flare session = %q", got)
	}
	if got := w.(interface{ PostLoginURL() string }).PostLoginURL(); got != "" {
		t.Fatalf("post login url = %q", got)
	}
	if w.(interface{ CookiesIncomplete([]*http.Cookie) bool }).CookiesIncomplete(nil) {
		t.Fatal("cookies incomplete for tracker without checker")
	}
	cookies := []*http.Cookie{{Name: "bb_session", Value: "x", Path: "/a"}}
	w.(interface{ EnhanceCookies([]*http.Cookie) }).EnhanceCookies(cookies)
	if cookies[0].Path != "/a" {
		t.Fatalf("cookie changed: %+v", cookies[0])
	}
}
