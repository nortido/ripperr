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
	"testing"
	"time"

	"ripperr/internal/helper"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/client"
	"ripperr/internal/tracker/client/rutor"
)

const rotationLink = "https://rutor.info/torrent/1"

type rotationTransport struct {
	down map[string]bool
}

func (r *rotationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.down[req.URL.Host] {
		return nil, errors.New("connection refused")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("<html>page from " + req.URL.Host + "</html>")),
		Request:    req,
	}, nil
}

type rotationEnv struct {
	s      *service
	rt     *rotationTransport
	now    time.Time
	probes map[string]int
	dead   map[string]bool
	hooks  [][3]string
}

type rotationOpts struct {
	mirrors     []string
	active      string
	hourLimit   int
	maxFailures int
}

func newRotationEnv(t *testing.T, o rotationOpts) *rotationEnv {
	t.Helper()
	if o.maxFailures == 0 {
		o.maxFailures = 5
	}
	tr := rutor.New(trackerModel.Tracker{
		Name:         "rutor",
		Domain:       "rutor.info",
		Publicity:    trackerModel.PublicityPublic,
		Enabled:      true,
		Mirrors:      o.mirrors,
		ActiveDomain: o.active,
		HourLimit:    o.hourLimit,
	})
	e := &rotationEnv{
		rt:     &rotationTransport{down: map[string]bool{}},
		now:    time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
		probes: map[string]int{},
		dead:   map[string]bool{},
	}
	s := newBuildCacheService(map[int]client.ITracker{RutorID: tr})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.hc = &http.Client{Transport: e.rt, Jar: jar}
	s.downloadHC = &http.Client{Transport: e.rt}
	s.cbCfg = &helper.CircuitBreakerConfig{MaxFailures: o.maxFailures}
	s.domainAlive = func(client.ITracker) domainAliveFunc {
		return func(_ context.Context, domain string) bool {
			e.probes[domain]++
			return !e.dead[domain]
		}
	}
	s.rot.now = func() time.Time { return e.now }
	s.SetMirrorSwitchHook(func(name, from, to string) {
		e.hooks = append(e.hooks, [3]string{name, from, to})
	})
	e.s = s
	return e
}

func (e *rotationEnv) page(t *testing.T) (string, error) {
	t.Helper()
	buf, err := e.s.Response(context.Background(), RutorID, rotationLink, false)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (e *rotationEnv) failPages(t *testing.T, n int, step time.Duration) {
	t.Helper()
	for range n {
		if _, err := e.page(t); err == nil {
			t.Fatal("expected page error")
		}
		e.now = e.now.Add(step)
	}
}

func (e *rotationEnv) domain() string {
	return e.s.clientIndex[RutorID].GetDomain()
}

func TestMirrorFailureClassifies(t *testing.T) {
	netErr := fmt.Errorf("rutor error: %w", &url.Error{Op: "Get", URL: "https://rutor.info", Err: errors.New("dial tcp: refused")})
	ctl := string(rune(0x7f))
	_, parseErr := url.Parse("http://rutor.info/" + ctl)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name   string
		ctx    context.Context
		err    error
		flare  bool
		expect bool
	}{
		{"direct transport", context.Background(), netErr, false, true},
		{"flare transport", context.Background(), netErr, true, false},
		{"direct 503", context.Background(), fmt.Errorf("x: %w", &client.StatusError{Code: 503}), false, true},
		{"flare 503", context.Background(), fmt.Errorf("x: %w", &client.StatusError{Code: 503}), true, true},
		{"403", context.Background(), &client.StatusError{Code: 403}, false, false},
		{"404", context.Background(), &client.StatusError{Code: 404}, true, false},
		{"circuit open", context.Background(), fmt.Errorf("x: %w", helper.ErrCircuitOpen), false, false},
		{"cancelled caller", cancelled, netErr, false, false},
		{"nil", context.Background(), nil, false, false},
		{"url parse error", context.Background(), fmt.Errorf("x: %w", parseErr), false, false},
	}
	for _, c := range cases {
		if got := mirrorFailure(c.ctx, c.err, c.flare); got != c.expect {
			t.Errorf("%s: got %v, want %v", c.name, got, c.expect)
		}
	}
}

func TestResponseRotatesAfterTwoNetworkFailures(t *testing.T) {
	e := newRotationEnv(t, rotationOpts{mirrors: []string{"rutor.is"}})
	e.rt.down["rutor.info"] = true

	if _, err := e.page(t); err == nil {
		t.Fatal("first failure must be returned")
	}
	got, err := e.page(t)
	if err != nil {
		t.Fatalf("second call must succeed on the mirror: %v", err)
	}
	if !strings.Contains(got, "rutor.is") {
		t.Fatalf("page = %q", got)
	}
	if e.domain() != "rutor.is" {
		t.Fatalf("domain = %q", e.domain())
	}
	if len(e.hooks) != 1 || e.hooks[0] != [3]string{"rutor", "rutor.info", "rutor.is"} {
		t.Fatalf("hooks = %v", e.hooks)
	}
}

func TestResponseNoRotationOutsideWindow(t *testing.T) {
	e := newRotationEnv(t, rotationOpts{mirrors: []string{"rutor.is"}, hourLimit: 30})
	e.rt.down["rutor.info"] = true

	e.failPages(t, 2, 6*time.Minute)
	if len(e.hooks) != 0 || e.domain() != "rutor.info" {
		t.Fatalf("hooks = %v domain = %q", e.hooks, e.domain())
	}
}

func TestRotationWindowFollowsHourLimit(t *testing.T) {
	e := newRotationEnv(t, rotationOpts{mirrors: []string{"rutor.is"}, hourLimit: 6})
	e.rt.down["rutor.info"] = true

	e.failPages(t, 1, 15*time.Minute)
	if _, err := e.page(t); err != nil {
		t.Fatalf("want rotation within 20 min window: %v", err)
	}
	if e.domain() != "rutor.is" {
		t.Fatalf("domain = %q", e.domain())
	}
}

func TestRotationThresholdFollowsBreaker(t *testing.T) {
	e := newRotationEnv(t, rotationOpts{mirrors: []string{"rutor.is"}, maxFailures: 1})
	e.rt.down["rutor.info"] = true

	if _, err := e.page(t); err != nil {
		t.Fatalf("want rotation on the first failure: %v", err)
	}
	if e.domain() != "rutor.is" {
		t.Fatalf("domain = %q", e.domain())
	}
}

func TestRotationCooldown(t *testing.T) {
	e := newRotationEnv(t, rotationOpts{mirrors: []string{"rutor.is"}})
	e.rt.down["rutor.info"] = true
	e.failPages(t, 1, 0)
	if _, err := e.page(t); err != nil {
		t.Fatal(err)
	}
	e.rt.down["rutor.info"] = false
	e.rt.down["rutor.is"] = true
	probes := e.probes["rutor.info"]

	e.now = e.now.Add(time.Minute)
	e.failPages(t, 2, time.Minute)
	if len(e.hooks) != 1 || e.domain() != "rutor.is" {
		t.Fatalf("switched during cooldown: hooks = %v domain = %q", e.hooks, e.domain())
	}
	if e.probes["rutor.info"] != probes {
		t.Fatalf("probes during cooldown: %d -> %d", probes, e.probes["rutor.info"])
	}

	e.now = e.now.Add(8 * time.Minute)
	e.failPages(t, 1, 30*time.Second)
	if _, err := e.page(t); err != nil {
		t.Fatalf("want switch back after cooldown: %v", err)
	}
	if e.domain() != "rutor.info" || len(e.hooks) != 2 || e.hooks[1] != [3]string{"rutor", "rutor.is", "rutor.info"} {
		t.Fatalf("domain = %q hooks = %v", e.domain(), e.hooks)
	}
}

func TestNoRotationWithActiveDomain(t *testing.T) {
	e := newRotationEnv(t, rotationOpts{mirrors: []string{"rutor.is"}, active: "rutor.is"})
	e.rt.down["rutor.is"] = true

	e.failPages(t, 3, time.Second)
	if len(e.hooks) != 0 || e.domain() != "rutor.is" {
		t.Fatalf("hooks = %v domain = %q", e.hooks, e.domain())
	}
	if len(e.probes) != 0 {
		t.Fatalf("probes = %v", e.probes)
	}
}

func TestNoRotationWithoutMirrors(t *testing.T) {
	e := newRotationEnv(t, rotationOpts{})
	e.rt.down["rutor.info"] = true

	e.failPages(t, 3, time.Second)
	if len(e.hooks) != 0 || len(e.probes) != 0 {
		t.Fatalf("hooks = %v probes = %v", e.hooks, e.probes)
	}
}

func TestNoRotationWhenNoCandidateAlive(t *testing.T) {
	e := newRotationEnv(t, rotationOpts{mirrors: []string{"rutor.is"}})
	e.rt.down["rutor.info"] = true
	e.dead["rutor.is"] = true

	e.failPages(t, 2, time.Second)
	if len(e.hooks) != 0 || e.domain() != "rutor.info" {
		t.Fatalf("hooks = %v domain = %q", e.hooks, e.domain())
	}
	probes := e.probes["rutor.is"]
	if probes == 0 {
		t.Fatal("candidate must be probed")
	}
	e.failPages(t, 2, time.Second)
	if e.probes["rutor.is"] != probes {
		t.Fatalf("cooldown must stop probing: %d -> %d", probes, e.probes["rutor.is"])
	}
}

func TestDownloadFailuresDoNotRotate(t *testing.T) {
	e := newRotationEnv(t, rotationOpts{mirrors: []string{"rutor.is"}})
	if _, err := e.page(t); err != nil {
		t.Fatal(err)
	}
	e.rt.down["rutor.info"] = true

	for range 3 {
		if _, err := e.s.Response(context.Background(), RutorID, "https://rutor.info/download/1", true); err == nil {
			t.Fatal("expected download error")
		}
	}
	if len(e.hooks) != 0 || e.probes["rutor.is"] != 0 || e.domain() != "rutor.info" {
		t.Fatalf("hooks = %v probes = %v domain = %q", e.hooks, e.probes, e.domain())
	}
}

func TestSuccessClearsFailures(t *testing.T) {
	e := newRotationEnv(t, rotationOpts{mirrors: []string{"rutor.is"}})
	e.rt.down["rutor.info"] = true
	e.failPages(t, 1, time.Second)
	e.rt.down["rutor.info"] = false
	if _, err := e.page(t); err != nil {
		t.Fatal(err)
	}
	e.rt.down["rutor.info"] = true
	e.failPages(t, 1, time.Second)
	if len(e.hooks) != 0 || e.domain() != "rutor.info" {
		t.Fatalf("hooks = %v domain = %q", e.hooks, e.domain())
	}
}

func TestTTLRebuildKeepsStickyMirror(t *testing.T) {
	e := newRotationEnv(t, rotationOpts{mirrors: []string{"rutor.is"}})
	e.rt.down["rutor.info"] = true
	e.failPages(t, 1, 0)
	if _, err := e.page(t); err != nil {
		t.Fatal(err)
	}

	e.s.clientIndex[RutorID].Created = time.Now().Add(-2 * TTL)
	if _, err := e.s.getCached(context.Background(), RutorID); err != nil {
		t.Fatal(err)
	}
	if e.domain() != "rutor.is" {
		t.Fatalf("sticky mirror lost: domain = %q", e.domain())
	}

	e.dead["rutor.is"] = true
	e.s.clientIndex[RutorID].Created = time.Now().Add(-2 * TTL)
	if _, err := e.s.getCached(context.Background(), RutorID); err != nil {
		t.Fatal(err)
	}
	if e.domain() != "rutor.info" {
		t.Fatalf("dead sticky must fall back to selection: domain = %q", e.domain())
	}
}
