package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"ripperr/internal/helper"
	"ripperr/internal/helper/tlscapture"
	"ripperr/internal/helper/tlsclient"
	"ripperr/internal/model/tracker"
)

type flakyIdentityProxy struct {
	*identityProxy
	fail atomic.Bool
}

func (p *flakyIdentityProxy) ChromeIdentity(ctx context.Context) (tlscapture.Identity, error) {
	if p.fail.Load() {
		return tlscapture.Identity{}, errors.New("flaresolverr is down")
	}
	return p.identityProxy.ChromeIdentity(ctx)
}

func initTLSClient(t *testing.T, px *flakyIdentityProxy, sess *TLSSession) IClient {
	t.Helper()
	c := newTLSClient(tlsJarClient(t), publicTLSTracker(), px, sess, nil)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTLSModeIdentityErrorKeepsAppliedIdentity(t *testing.T) {
	f := newDoerFactory()
	px := &flakyIdentityProxy{identityProxy: &identityProxy{ids: []tlscapture.Identity{builtinIdentity()}}}
	sess := NewTLSSession(f.build)
	for range 2 {
		c := initTLSClient(t, px, sess)
		res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
		if err != nil {
			t.Fatal(err)
		}
		readAll(t, res)
		px.fail.Store(true)
	}
	reqs := f.one.requests()
	if got := len(f.configs()); got != 1 {
		t.Fatalf("builds = %d, want 1", got)
	}
	for i, r := range reqs {
		if ua := r.header.Get("User-Agent"); ua != uaLinux152 || strings.Contains(ua, "Chrome/140") {
			t.Fatalf("request %d user-agent = %q", i, ua)
		}
	}
}

func TestTLSModeIdentityErrorKeepsFlareOnly(t *testing.T) {
	flareID := builtinIdentity()
	flareID.Mode = tlscapture.ModeFlare
	f := newDoerFactory()
	px := &flakyIdentityProxy{identityProxy: &identityProxy{ids: []tlscapture.Identity{flareID}}}
	sess := NewTLSSession(f.build)
	initTLSClient(t, px, sess)
	px.fail.Store(true)
	c := initTLSClient(t, px, sess)
	if !c.(interface{ EncodedBody() bool }).EncodedBody() {
		t.Fatal("flare-only identity was dropped after an identity error")
	}
	if len(f.configs()) != 0 {
		t.Fatalf("builds = %d", len(f.configs()))
	}
}

func TestTLSModeIdentityErrorOnFreshSessionFallsBack(t *testing.T) {
	f := newDoerFactory()
	px := &flakyIdentityProxy{identityProxy: &identityProxy{ids: []tlscapture.Identity{builtinIdentity()}}}
	px.fail.Store(true)
	c := initTLSClient(t, px, NewTLSSession(f.build))
	res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	if ua := f.one.requests()[0].header.Get("User-Agent"); !strings.Contains(ua, "Chrome/140") {
		t.Fatalf("user-agent = %q", ua)
	}
}

func TestTLSModeSolveCancelKeepsContextError(t *testing.T) {
	s := newChallengeSetup(t)
	s.f.one.reply = func(int, *http.Request) *http.Response { return headerChallenge("blocked") }
	ctx, cancel := context.WithCancel(t.Context())
	s.px.send = func(*http.Request) (*http.Response, error) {
		cancel()
		return nil, &url.Error{Op: "Post", URL: "http://flare:8191/v1", Err: context.Canceled}
	}
	_, err := s.c.GetResponse(ctx, "https://tls.example/page", false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		t.Fatalf("url.Error leaked through: %v", err)
	}
}

func (f *doerFactory) failing(fail *atomic.Bool) func(tlsclient.Config) (helper.HTTPDoer, error) {
	return func(cfg tlsclient.Config) (helper.HTTPDoer, error) {
		if fail.Load() {
			return nil, errors.New("build failed")
		}
		return f.build(cfg)
	}
}

func TestTLSModeFirstBuildFailureLeavesSessionFresh(t *testing.T) {
	f := newDoerFactory()
	var fail atomic.Bool
	fail.Store(true)
	px := &flakyIdentityProxy{identityProxy: &identityProxy{ids: []tlscapture.Identity{builtinIdentity()}}}
	sess := NewTLSSession(f.failing(&fail))
	first := newTLSClient(tlsJarClient(t), publicTLSTracker(), px, sess, nil)
	if err := first.InitClient(t.Context(), &tracker.Tracker{}, false); err == nil {
		t.Fatal("want the build error")
	}
	if sess.isApplied() {
		t.Fatal("session is applied after a failed first build")
	}
	fail.Store(false)
	px.fail.Store(true)
	c := initTLSClient(t, px, sess)
	res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	if ua := f.one.requests()[0].header.Get("User-Agent"); !strings.Contains(ua, "Chrome/140") {
		t.Fatalf("user-agent = %q, want the fallback identity", ua)
	}
}

func TestTLSModeFailedRebuildKeepsOldDoerAndHeaders(t *testing.T) {
	f := newDoerFactory()
	var fail atomic.Bool
	newID := builtinIdentity()
	fp := capturedFingerprint()
	fp.Headers["user-agent"] = uaLinux153
	newID.UserAgent, newID.Mode, newID.Fingerprint = uaLinux153, tlscapture.ModeCustom, fp
	px := &flakyIdentityProxy{identityProxy: &identityProxy{ids: []tlscapture.Identity{builtinIdentity(), newID}}}
	sess := NewTLSSession(f.failing(&fail))
	c := initTLSClient(t, px, sess)
	fail.Store(true)
	next := newTLSClient(tlsJarClient(t), publicTLSTracker(), px, sess, nil)
	if err := next.InitClient(t.Context(), &tracker.Tracker{}, false); err == nil {
		t.Fatal("want the build error")
	}
	res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	if ua := f.one.requests()[0].header.Get("User-Agent"); ua != uaLinux152 {
		t.Fatalf("user-agent = %q, want the old identity", ua)
	}
}
