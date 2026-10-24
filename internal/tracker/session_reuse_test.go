package tracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/helper/tlsclient"
	proxyModel "ripperr/internal/model/proxy"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/client"
	"ripperr/internal/tracker/client/rutracker"
)

type recordingProxy struct {
	mu        sync.Mutex
	setCookie string
	logins    int
	destroys  int
}

func (p *recordingProxy) Send(r *http.Request, _ *proxyModel.Options, _ proxyModel.ProxyType) (*http.Response, error) {
	if r.Method == http.MethodPost {
		p.mu.Lock()
		p.logins++
		p.mu.Unlock()
	}
	h := http.Header{}
	if p.setCookie != "" {
		h.Add("Set-Cookie", p.setCookie)
	}
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("<html>ok</html>")), Request: r, Header: h}, nil
}

func (p *recordingProxy) DestroyFlareSession(context.Context, string) error {
	p.mu.Lock()
	p.destroys++
	p.mu.Unlock()
	return nil
}

func (p *recordingProxy) counts() (logins, destroys int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.logins, p.destroys
}

type savedCookiesDB struct {
	creds string
	mu    sync.Mutex
	saved []string
}

func (d *savedCookiesDB) GetTracker(_ context.Context, id int) (*trackerModel.Tracker, error) {
	return &trackerModel.Tracker{
		TypeID:      id,
		Exists:      true,
		Credentials: d.creds,
		Cookies:     []*http.Cookie{{Name: "bb_session", Value: "saved"}},
	}, nil
}

func (d *savedCookiesDB) SaveTracker(_ context.Context, tr *trackerModel.Tracker) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.saved = append(d.saved, helper.CookiesString(tr.Cookies))
	return nil
}

type scriptedDownloads struct {
	mu    sync.Mutex
	calls int
	first func(*http.Request) (*http.Response, error)
}

func (d *scriptedDownloads) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.calls++
	n := d.calls
	d.mu.Unlock()
	if n == 1 {
		return d.first(req)
	}
	return downloadReply(req, http.StatusOK, strings.Repeat("d8:announce", 10)), nil
}

func (d *scriptedDownloads) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func downloadReply(req *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Request: req, Header: http.Header{}}
}

func newReuseService(t *testing.T, dl helper.HTTPDoer) (*service, *recordingProxy) {
	t.Helper()
	tr := rutracker.New(trackerModel.Tracker{
		Name:        "rutracker",
		Domain:      "rutracker.org",
		ProxyType:   proxyModel.TypeFlare,
		Publicity:   "private",
		LoginURL:    "https://rutracker.org/forum/login.php",
		Credentials: "login_username=x&login_password=y",
		Enabled:     true,
	})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	px := &recordingProxy{}
	s := newBuildCacheService(map[int]client.ITracker{RutrackerID: tr})
	s.hc = &http.Client{Timeout: 5 * time.Second, Jar: jar}
	s.downloadHC = dl
	s.px = px
	s.db = &savedCookiesDB{creds: tr.GetCredentials()}
	s.cbCfg = &helper.CircuitBreakerConfig{MaxFailures: 100, Timeout: time.Minute}
	return s, px
}

func ageCached(s *service, id int) {
	s.mx.Lock()
	s.clientIndex[id].Created = time.Now().Add(-2 * TTL)
	s.mx.Unlock()
}

func TestTTLRebuildReusesCookies(t *testing.T) {
	s, px := newReuseService(t, nil)
	ctx := context.Background()
	if _, err := s.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	if l, d := px.counts(); l != 0 || d != 0 {
		t.Fatalf("start must reuse saved cookies: logins=%d destroys=%d", l, d)
	}

	ageCached(s, RutrackerID)
	if _, err := s.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	if l, d := px.counts(); l != 0 || d != 0 {
		t.Fatalf("hourly rebuild must reuse cookies and keep the flare session: logins=%d destroys=%d", l, d)
	}
}

func TestTTLRebuildLogsInAfterLoginMaxAge(t *testing.T) {
	s, px := newReuseService(t, nil)
	ctx := context.Background()
	if _, err := s.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}

	ageCached(s, RutrackerID)
	s.loginClock().set(RutrackerID, time.Now().Add(-loginMaxAge-time.Minute))
	if _, err := s.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	if l, d := px.counts(); l != 1 || d == 0 {
		t.Fatalf("rebuild older than loginMaxAge must log in and destroy the session: logins=%d destroys=%d", l, d)
	}

	ageCached(s, RutrackerID)
	if _, err := s.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	if l, _ := px.counts(); l != 1 {
		t.Fatalf("the forced login must restart the 24 h window: logins=%d", l)
	}
}

func TestResumeAfterPauseForcesLogin(t *testing.T) {
	s, px := newReuseService(t, nil)
	ctx := context.Background()
	if _, err := s.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}

	s.rot.mu.Lock()
	s.rot.paused = map[int]mirrorPause{RutrackerID: {host: "mirror.example", until: time.Now().Add(-time.Minute)}}
	s.rot.mu.Unlock()
	if _, err := s.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	if l, d := px.counts(); l != 1 || d == 0 {
		t.Fatalf("resume after a pause must force the login and destroy the session: logins=%d destroys=%d", l, d)
	}
}

func TestDownloadErrorsWithoutSessionMeaningKeepLogin(t *testing.T) {
	cases := map[string]func(*http.Request) (*http.Response, error){
		"503": func(r *http.Request) (*http.Response, error) {
			return downloadReply(r, http.StatusServiceUnavailable, "x"), nil
		},
		"404":         func(r *http.Request) (*http.Response, error) { return downloadReply(r, http.StatusNotFound, "x"), nil },
		"deadline":    func(*http.Request) (*http.Response, error) { return nil, context.DeadlineExceeded },
		"canceled":    func(*http.Request) (*http.Response, error) { return nil, context.Canceled },
		"circuit":     func(*http.Request) (*http.Response, error) { return nil, helper.ErrCircuitOpen },
		"unavailable": func(*http.Request) (*http.Response, error) { return nil, client.ErrTrackerUnavailable },
		"net timeout": func(*http.Request) (*http.Response, error) { return nil, timeoutErr{} },
	}
	for name, first := range cases {
		t.Run(name, func(t *testing.T) {
			dl := &scriptedDownloads{first: first}
			s, px := newReuseService(t, dl)
			ctx := context.Background()
			if _, err := s.getCached(ctx, RutrackerID); err != nil {
				t.Fatal(err)
			}
			buf, err := s.Response(ctx, RutrackerID, "https://rutracker.org/forum/dl.php?t=1", true)
			if err == nil || buf != nil {
				t.Fatalf("the download error must be returned: buf=%v err=%v", buf, err)
			}
			if n := dl.count(); n != 1 {
				t.Fatalf("no retry expected, downloads=%d", n)
			}
			if l, d := px.counts(); l != 0 || d != 0 {
				t.Fatalf("no relogin expected: logins=%d destroys=%d", l, d)
			}
		})
	}
}

func TestDownloadSessionFailureStillRelogins(t *testing.T) {
	cases := map[string]func(*http.Request) (*http.Response, error){
		"html": func(r *http.Request) (*http.Response, error) {
			return downloadReply(r, http.StatusOK, "<html>login</html>"+strings.Repeat(" ", 60)), nil
		},
		"short":    func(r *http.Request) (*http.Response, error) { return downloadReply(r, http.StatusOK, "x"), nil },
		"403":      func(r *http.Request) (*http.Response, error) { return downloadReply(r, http.StatusForbidden, "x"), nil },
		"redirect": func(r *http.Request) (*http.Response, error) { return downloadReply(r, http.StatusFound, "x"), nil },
		"unknown":  func(*http.Request) (*http.Response, error) { return nil, errors.New("boom") },
		"401 plain": func(r *http.Request) (*http.Response, error) {
			return downloadReply(r, http.StatusUnauthorized, "x"), nil
		},
	}
	for name, first := range cases {
		t.Run(name, func(t *testing.T) {
			dl := &scriptedDownloads{first: first}
			s, px := newReuseService(t, dl)
			ctx := context.Background()
			if _, err := s.getCached(ctx, RutrackerID); err != nil {
				t.Fatal(err)
			}
			buf, err := s.Response(ctx, RutrackerID, "https://rutracker.org/forum/dl.php?t=1", true)
			if err != nil || buf == nil {
				t.Fatalf("the retry after relogin must succeed: err=%v", err)
			}
			if n := dl.count(); n != 2 {
				t.Fatalf("expected one retry, downloads=%d", n)
			}
			if l, _ := px.counts(); l != 1 {
				t.Fatalf("expected one login, logins=%d", l)
			}
		})
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestMissForcesLoginOnNextRequest(t *testing.T) {
	s, px := newReuseService(t, nil)
	ctx := context.Background()
	if _, err := s.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	s.loginClock().set(RutrackerID, time.Now().Add(-2*time.Hour))

	s.ForceLoginNext(RutrackerID)
	if _, err := s.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	if l, d := px.counts(); l != 1 || d == 0 {
		t.Fatalf("the request after a guest miss must log in: logins=%d destroys=%d", l, d)
	}

	for range 3 {
		s.ForceLoginNext(RutrackerID)
		if _, err := s.getCached(ctx, RutrackerID); err != nil {
			t.Fatal(err)
		}
	}
	if l, _ := px.counts(); l != 1 {
		t.Fatalf("repeated misses within an hour must not log in again: logins=%d", l)
	}
}

func TestMissRightAfterStartKeepsCookies(t *testing.T) {
	s, px := newReuseService(t, nil)
	ctx := context.Background()
	if _, err := s.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	s.ForceLoginNext(RutrackerID)
	if _, err := s.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	if l, d := px.counts(); l != 0 || d != 0 {
		t.Fatalf("a login less than an hour ago must not repeat: logins=%d destroys=%d", l, d)
	}
}

func TestForceLoginNextUnknownTrackerIsNoop(t *testing.T) {
	s, _ := newReuseService(t, nil)
	s.ForceLoginNext(999)
}

func TestRedirectRefusalIsNotSessionFailure(t *testing.T) {
	refusal := fmt.Errorf("%w: redirect to http", tlsclient.ErrRedirectRefused)
	err := &url.Error{Op: "Get", URL: "https://x/dl", Err: refusal}
	if sessionSuspect(err) {
		t.Fatal("a refused redirect says nothing about the session")
	}
}

func absorbFresh(t *testing.T, ct *CachedTracker, px *recordingProxy) {
	t.Helper()
	px.setCookie = "bb_session=fresh"
	res, err := ct.GetResponse(context.Background(), "https://rutracker.org/forum/viewtopic.php?t=1", false)
	px.setCookie = ""
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
}

func cookieValues(c client.IClient) (vals []string) {
	for _, ck := range c.Cookies() {
		vals = append(vals, ck.Value)
	}
	return
}

func TestNonForcedRebuildKeepsAbsorbedCookies(t *testing.T) {
	s, px := newReuseService(t, nil)
	ctx := context.Background()
	ct, err := s.getCached(ctx, RutrackerID)
	if err != nil {
		t.Fatal(err)
	}
	absorbFresh(t, ct, px)

	ageCached(s, RutrackerID)
	next, err := s.getCached(ctx, RutrackerID)
	if err != nil {
		t.Fatal(err)
	}
	if got := cookieValues(next); len(got) != 1 || got[0] != "fresh" {
		t.Fatalf("rebuild must keep the cookies absorbed during the hour, got %v", got)
	}
	db := s.db.(*savedCookiesDB)
	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.saved) != 1 || !strings.Contains(db.saved[0], "fresh") {
		t.Fatalf("the fresh cookies must reach the DB, saved=%v", db.saved)
	}
}

func TestForcedLoginStartsWithoutOldCookies(t *testing.T) {
	s, px := newReuseService(t, nil)
	ctx := context.Background()
	ct, err := s.getCached(ctx, RutrackerID)
	if err != nil {
		t.Fatal(err)
	}
	absorbFresh(t, ct, px)

	ageCached(s, RutrackerID)
	s.loginClock().set(RutrackerID, time.Now().Add(-loginMaxAge-time.Minute))
	next, err := s.getCached(ctx, RutrackerID)
	if err != nil {
		t.Fatal(err)
	}
	if got := cookieValues(next); len(got) != 0 {
		t.Fatalf("a forced login must start clean, got %v", got)
	}
}

func TestRebuildWhileOldClientAbsorbsCookies(t *testing.T) {
	s, px := newReuseService(t, nil)
	ctx := context.Background()
	ct, err := s.getCached(ctx, RutrackerID)
	if err != nil {
		t.Fatal(err)
	}
	px.setCookie = "bb_session=fresh"

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			res, err := ct.GetResponse(ctx, "https://rutracker.org/forum/viewtopic.php?t=1", false)
			if err != nil {
				t.Error(err)
				return
			}
			_ = res.Body.Close()
		}
	})
	for range 50 {
		ageCached(s, RutrackerID)
		if _, err := s.getCached(ctx, RutrackerID); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}

func TestRedirectLoopStaysSessionFailure(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://x/dl", Err: errors.New("stopped after 10 redirects")}
	if !sessionSuspect(err) {
		t.Fatal("a redirect loop can be dead cookies bouncing to the login page")
	}
}

func TestReloadKeepsLoginWindow(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name       string
		lastLogin  time.Duration
		wantLogins int
	}{
		{"older than loginMaxAge", loginMaxAge + time.Hour, 1},
		{"within loginMaxAge", 2 * time.Hour, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newReuseService(t, nil)
			if _, err := a.getCached(ctx, RutrackerID); err != nil {
				t.Fatal(err)
			}
			a.loginClock().set(RutrackerID, time.Now().Add(-tc.lastLogin))

			b, px := newReuseService(t, nil)
			b.logins = a.logins
			if _, err := b.getCached(ctx, RutrackerID); err != nil {
				t.Fatal(err)
			}
			ageCached(b, RutrackerID)
			if _, err := b.getCached(ctx, RutrackerID); err != nil {
				t.Fatal(err)
			}
			if l, _ := px.counts(); l != tc.wantLogins {
				t.Fatalf("logins after reload = %d, want %d", l, tc.wantLogins)
			}
		})
	}
}

func TestReloadKeepsMissGap(t *testing.T) {
	ctx := context.Background()
	a, _ := newReuseService(t, nil)
	if _, err := a.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	a.loginClock().set(RutrackerID, time.Now().Add(-10*time.Minute))

	b, px := newReuseService(t, nil)
	b.logins = a.logins
	if _, err := b.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	b.ForceLoginNext(RutrackerID)
	if _, err := b.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	if l, _ := px.counts(); l != 0 {
		t.Fatalf("a login 10 minutes ago in the previous service must not repeat: logins=%d", l)
	}
}

func TestReloadMissAfterGapForcesLogin(t *testing.T) {
	ctx := context.Background()
	a, _ := newReuseService(t, nil)
	if _, err := a.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	a.loginClock().set(RutrackerID, time.Now().Add(-3*time.Hour))

	b, px := newReuseService(t, nil)
	b.logins = a.logins
	if _, err := b.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	b.ForceLoginNext(RutrackerID)
	if _, err := b.getCached(ctx, RutrackerID); err != nil {
		t.Fatal(err)
	}
	if l, _ := px.counts(); l != 1 {
		t.Fatalf("a miss 3 hours after the last login must force one: logins=%d", l)
	}
}

func TestNewServicesShareLoginTimes(t *testing.T) {
	saved := processLogins
	processLogins = &loginTimes{}
	t.Cleanup(func() { processLogins = saved })

	hc := &http.Client{Timeout: time.Second}
	build := func() *service {
		return New(hc, hc, stubFlareProxy{}, buildCacheDB{}, &helper.CircuitBreakerConfig{}, trackerModel.TrackerConfig{}, nil)
	}
	a, b := build(), build()
	at := time.Now().Add(-5 * time.Hour).Truncate(time.Second)
	a.loginClock().set(RutrackerID, at)
	if got, ok := b.loginClock().get(RutrackerID); !ok || !got.Equal(at) {
		t.Fatalf("a service built after a reload must see the previous login time: got %v, %v", got, ok)
	}
}
