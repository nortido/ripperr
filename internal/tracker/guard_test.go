package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/helper"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/client"
	"ripperr/internal/tracker/client/kinozal"
)

const (
	guardLink   = "https://kinozal.tv/details.php?id=1"
	guardHost   = "x.example"
	guardSecret = "secret"
	guardCreds  = "username=u&password=" + guardSecret
)

type guardRequest struct {
	method, host, path, cookie, body string
}

type guardTransport struct {
	mu       sync.Mutex
	down     map[string]bool
	pageDown map[string]bool
	reqs     []guardRequest
}

func (g *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	g.mu.Lock()
	g.reqs = append(g.reqs, guardRequest{req.Method, req.URL.Host, req.URL.Path, req.Header.Get("Cookie"), string(body)})
	down := g.down[req.URL.Host] || (g.pageDown[req.URL.Host] && req.URL.Path != "" && req.URL.Path != "/")
	g.mu.Unlock()
	if down {
		return nil, errors.New("connection refused")
	}
	h := http.Header{}
	if req.Method == http.MethodPost {
		h.Add("Set-Cookie", "uid=1; Path=/")
		h.Add("Set-Cookie", "pass=2; Path=/")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader("<html>page from " + req.URL.Host + "</html>")),
		Request:    req,
	}, nil
}

func (g *guardTransport) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.reqs)
}

func (g *guardTransport) to(host string) []guardRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []guardRequest
	for _, r := range g.reqs {
		if r.host == host {
			out = append(out, r)
		}
	}
	return out
}

func assertAnonymousOnly(t *testing.T, reqs []guardRequest) {
	t.Helper()
	for _, r := range reqs {
		if r.method != http.MethodGet || (r.path != "" && r.path != "/") || r.cookie != "" || strings.Contains(r.body, guardSecret) {
			t.Fatalf("non-anonymous request to unconfirmed host: %+v", r)
		}
	}
}

type pauseCall struct {
	ttid int
	host string
}

type guardEnv struct {
	s        *service
	rt       *guardTransport
	now      time.Time
	pauses   []pauseCall
	switches [][3]string
}

func newGuardEnv(t *testing.T, unconfirmed []string, active string) *guardEnv {
	t.Helper()
	tr := kinozal.New(trackerModel.Tracker{
		Name:         "kinozal",
		Domain:       "kinozal.tv",
		ActiveDomain: active,
		Mirrors:      []string{"kinozal.guru", guardHost},
		Unconfirmed:  unconfirmed,
		Publicity:    "private",
		LoginURL:     "https://kinozal.tv/takelogin.php",
		Credentials:  guardCreds,
		Enabled:      true,
	})
	e := &guardEnv{
		rt:  &guardTransport{down: map[string]bool{}},
		now: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
	}
	s := newBuildCacheService(map[int]client.ITracker{KinozalID: tr})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.hc = &http.Client{
		Transport:     e.rt,
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	s.downloadHC = &http.Client{Transport: e.rt}
	s.cbCfg = &helper.CircuitBreakerConfig{MaxFailures: 5}
	s.domainAlive = domainProbeWith(s.hc)
	s.rot.now = func() time.Time { return e.now }
	s.SetMirrorSwitchHook(func(name, from, to string) {
		e.switches = append(e.switches, [3]string{name, from, to})
	})
	s.SetMirrorPauseHook(func(ttid int, host string) {
		e.pauses = append(e.pauses, pauseCall{ttid, host})
	})
	e.s = s
	return e
}

func (e *guardEnv) page() (string, error) {
	buf, err := e.s.Response(context.Background(), KinozalID, guardLink, false)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (e *guardEnv) confirmedDown(down bool) {
	e.rt.down["kinozal.tv"] = down
	e.rt.down["kinozal.guru"] = down
}

func (e *guardEnv) lastPause() pauseCall {
	if len(e.pauses) == 0 {
		return pauseCall{}
	}
	return e.pauses[len(e.pauses)-1]
}

func isPauseErr(err error) bool {
	return errors.Is(err, client.ErrTrackerUnavailable) && errors.Is(err, ErrMirrorUnconfirmed) && strings.Contains(err.Error(), guardHost)
}

func (e *guardEnv) pauseByRotate(t *testing.T) error {
	t.Helper()
	if _, err := e.page(); err != nil {
		t.Fatalf("first page: %v", err)
	}
	e.confirmedDown(true)
	if _, err := e.page(); err == nil {
		t.Fatal("want first network failure")
	}
	_, err := e.page()
	return err
}

func TestGuardRotateSkipsUnconfirmed(t *testing.T) {
	e := newGuardEnv(t, []string{guardHost}, "")
	if err := e.pauseByRotate(t); err == nil {
		t.Fatal("rotation must not switch to the unconfirmed host")
	}
	if len(e.switches) != 0 {
		t.Fatalf("switches = %v", e.switches)
	}
	if d := e.s.rot.sticky[KinozalID]; d != "" {
		t.Fatalf("sticky = %q", d)
	}
	reqs := e.rt.to(guardHost)
	if len(reqs) == 0 {
		t.Fatal("unconfirmed host must be probed")
	}
	assertAnonymousOnly(t, reqs)
	if got := e.lastPause(); got != (pauseCall{KinozalID, guardHost}) {
		t.Fatalf("pauses = %v", e.pauses)
	}
}

func TestPauseFromRotateIsTrackerWide(t *testing.T) {
	e := newGuardEnv(t, []string{guardHost}, "")
	err := e.pauseByRotate(t)
	if !isPauseErr(err) {
		t.Fatalf("err = %v", err)
	}
	if _, ok := e.s.clientIndex[KinozalID]; ok {
		t.Fatal("client on the dead host must leave the cache")
	}
	n := e.rt.count()
	ct, err := e.s.getCached(context.Background(), KinozalID)
	if ct != nil || !isPauseErr(err) {
		t.Fatalf("getCached = %v, %v", ct, err)
	}
	if e.rt.count() != n {
		t.Fatalf("paused getCached went to network: %d -> %d", n, e.rt.count())
	}
}

func TestPauseFromCacheSkipsStale(t *testing.T) {
	e := newGuardEnv(t, []string{guardHost}, "")
	if _, err := e.page(); err != nil {
		t.Fatal(err)
	}
	e.confirmedDown(true)
	e.s.clientIndex[KinozalID].Created = time.Now().Add(-2 * TTL)
	ct, err := e.s.getCached(context.Background(), KinozalID)
	if ct != nil || !isPauseErr(err) {
		t.Fatalf("getCached = %v, %v", ct, err)
	}
	assertAnonymousOnly(t, e.rt.to(guardHost))
	if got := e.lastPause(); got != (pauseCall{KinozalID, guardHost}) {
		t.Fatalf("pauses = %v", e.pauses)
	}
}

func TestPauseResumeAfterCooldown(t *testing.T) {
	e := newGuardEnv(t, []string{guardHost}, "")
	e.confirmedDown(true)
	if _, err := e.page(); !isPauseErr(err) {
		t.Fatalf("err = %v", err)
	}
	if len(e.pauses) != 1 || e.pauses[0] != (pauseCall{KinozalID, guardHost}) {
		t.Fatalf("pauses = %v", e.pauses)
	}

	n := e.rt.count()
	e.now = e.now.Add(9 * time.Minute)
	if _, err := e.page(); !isPauseErr(err) {
		t.Fatalf("+9 min: err = %v", err)
	}
	if e.rt.count() != n {
		t.Fatalf("+9 min: paused tracker went to network: %d -> %d", n, e.rt.count())
	}

	e.now = e.now.Add(time.Minute)
	e.rt.down["kinozal.tv"] = false
	got, err := e.page()
	if err != nil || !strings.Contains(got, "kinozal.tv") {
		t.Fatalf("+10 min: page = %q, err = %v", got, err)
	}
	logins := 0
	for _, r := range e.rt.to("kinozal.tv") {
		if r.method == http.MethodPost && strings.Contains(r.body, guardSecret) {
			logins++
		}
	}
	if logins != 1 {
		t.Fatalf("logins on kinozal.tv = %d", logins)
	}
	assertAnonymousOnly(t, e.rt.to(guardHost))
	if got := e.lastPause(); got != (pauseCall{KinozalID, ""}) || len(e.pauses) != 2 {
		t.Fatalf("pauses = %v", e.pauses)
	}
}

func TestConfirmedMirrorBackResumes(t *testing.T) {
	e := newGuardEnv(t, []string{guardHost}, "")
	if err := e.pauseByRotate(t); !isPauseErr(err) {
		t.Fatalf("err = %v", err)
	}
	e.now = e.now.Add(mirrorCooldown)
	e.rt.down["kinozal.guru"] = false
	got, err := e.page()
	if err != nil || !strings.Contains(got, "kinozal.guru") {
		t.Fatalf("page = %q, err = %v", got, err)
	}
	assertAnonymousOnly(t, e.rt.to(guardHost))
	if got := e.lastPause(); got != (pauseCall{KinozalID, ""}) {
		t.Fatalf("pauses = %v", e.pauses)
	}
}

func TestGuardOffRotatesToMirror(t *testing.T) {
	e := newGuardEnv(t, nil, "")
	if _, err := e.page(); err != nil {
		t.Fatal(err)
	}
	e.confirmedDown(true)
	if _, err := e.page(); err == nil {
		t.Fatal("want first network failure")
	}
	got, err := e.page()
	if err != nil || !strings.Contains(got, guardHost) {
		t.Fatalf("page = %q, err = %v", got, err)
	}
	if len(e.switches) != 1 || e.switches[0] != [3]string{"kinozal", "kinozal.tv", guardHost} {
		t.Fatalf("switches = %v", e.switches)
	}
}

func TestGuardKeepsChosenActiveDomain(t *testing.T) {
	e := newGuardEnv(t, []string{guardHost}, guardHost)
	e.rt.down["kinozal.tv"] = true
	got, err := e.page()
	if err != nil || !strings.Contains(got, guardHost) {
		t.Fatalf("page = %q, err = %v", got, err)
	}
	for _, p := range e.pauses {
		if p.host != "" {
			t.Fatalf("pauses = %v", e.pauses)
		}
	}
}

func TestTestLoginRefusesUnconfirmedOnly(t *testing.T) {
	rt := &guardTransport{down: map[string]bool{"kinozal.tv": true, "kinozal.guru": true}}
	prev := loginTransport
	loginTransport = func(string) http.RoundTripper { return rt }
	t.Cleanup(func() { loginTransport = prev })

	cfg := config.App{
		Runner: config.Runner{TLSProfile: "chrome_131", HttpTimeout: 5, MirrorConfirm: true},
		Tracker: config.Tracker{"kinozal": &config.TrackerItem{
			Name:        "kinozal",
			Domain:      "kinozal.tv",
			Mirrors:     []string{"kinozal.guru", guardHost},
			Unconfirmed: []string{guardHost},
			Publicity:   "private",
			LoginURL:    "https://kinozal.tv/takelogin.php",
			Credentials: guardCreds,
			Enabled:     true,
		}},
	}
	err := TestLogin(context.Background(), cfg, nopLoginDB{}, "kinozal", json.RawMessage(nil))
	want := "only the unconfirmed mirror " + guardHost + " answers, confirm it in the tracker settings"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v", err)
	}
	for _, r := range rt.reqs {
		if strings.Contains(r.body, guardSecret) {
			t.Fatalf("credentials sent: %+v", r)
		}
	}
	assertAnonymousOnly(t, rt.to(guardHost))
}

func TestGuardKeepsAnsweringCurrentHost(t *testing.T) {
	e := newGuardEnv(t, []string{guardHost}, "")
	if _, err := e.page(); err != nil {
		t.Fatal(err)
	}
	ct := e.s.clientIndex[KinozalID]
	e.rt.pageDown = map[string]bool{"kinozal.tv": true}
	e.rt.down["kinozal.guru"] = true
	for range 2 {
		if _, err := e.page(); err == nil || errors.Is(err, ErrMirrorUnconfirmed) {
			t.Fatalf("err = %v", err)
		}
	}
	if len(e.pauses) != 1 || e.pauses[0] != (pauseCall{KinozalID, ""}) {
		t.Fatalf("pauses = %v", e.pauses)
	}
	if got := e.s.clientIndex[KinozalID]; got != ct {
		t.Fatalf("client replaced: %v", got)
	}
	assertAnonymousOnly(t, e.rt.to(guardHost))
}
