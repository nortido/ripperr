package client

import (
	"net/http"
	"strings"
	"testing"

	"ripperr/internal/helper/tlscapture"
	"ripperr/internal/model/tracker"
)

const uaFirefox152 = "Mozilla/5.0 (X11; Linux x86_64; rv:152.0) Gecko/20100101 Firefox/152.0"

func firefoxClient(t *testing.T, dl *recDoer) (IClient, *doerFactory) {
	t.Helper()
	f := newDoerFactory()
	id := tlscapture.Identity{UserAgent: uaFirefox152, Profile: "firefox_148", Mode: tlscapture.ModeBuiltin}
	px := &identityProxy{ids: []tlscapture.Identity{id}}
	c := newTLSClient(tlsJarClient(t), publicTLSTracker(), px, NewTLSSession(f.build), dl)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	return c, f
}

func TestTLSModeFirefoxPageHasNoChromeHints(t *testing.T) {
	c, f := firefoxClient(t, nil)
	res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	h := f.one.requests()[0].header
	if got := h.Get("User-Agent"); got != uaFirefox152 {
		t.Fatalf("User-Agent = %q", got)
	}
	for _, k := range []string{"Sec-Ch-Ua", "Sec-Ch-Ua-Mobile", "Sec-Ch-Ua-Platform"} {
		if v, ok := h[k]; ok {
			t.Fatalf("%s = %q, Firefox does not send client hints", k, v)
		}
	}
	if strings.Contains(h.Get("Accept"), "signed-exchange") {
		t.Fatalf("Accept = %q, Chrome-only value", h.Get("Accept"))
	}
	cfg := f.configs()[0]
	if cfg.Profile != "firefox_148" || len(cfg.HeaderOrder) != 0 {
		t.Fatalf("profile=%q order=%v, want firefox_148 without Chrome header order", cfg.Profile, cfg.HeaderOrder)
	}
	if f.one == nil || len(f.one.requests()) != 1 {
		t.Fatal("page must go direct")
	}
}

func TestTLSModeFirefoxDownloadHasNoChromeHints(t *testing.T) {
	dl := &recDoer{}
	c, _ := firefoxClient(t, dl)
	res, err := c.GetResponse(t.Context(), "https://tls.example/dl?t=1", true)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	h := dl.requests()[0].header
	if got := h.Get("User-Agent"); got != uaFirefox152 {
		t.Fatalf("User-Agent = %q", got)
	}
	for _, k := range []string{"Sec-Ch-Ua", "Sec-Ch-Ua-Mobile", "Sec-Ch-Ua-Platform"} {
		if v, ok := h[k]; ok {
			t.Fatalf("%s = %q, Firefox does not send client hints", k, v)
		}
	}
}

func TestTLSModeSilentSolverTakesUAFromSolve(t *testing.T) {
	own := tlscapture.Identity{Mode: tlscapture.ModeBuiltin}
	firefox := tlscapture.Identity{UserAgent: uaFirefox152, Profile: "firefox_148", Mode: tlscapture.ModeBuiltin}
	s := newChallengeSetup(t, own, firefox)
	s.px.send = func(r *http.Request) (*http.Response, error) { return solveResponse(r, uaFirefox152), nil }
	s.f.one.reply = func(_ int, req *http.Request) *http.Response {
		if hasClearance(req) {
			return nil
		}
		return headerChallenge("blocked")
	}
	res, err := s.c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	if got := s.px.hints; len(got) != 2 || got[0] != "" || got[1] != uaFirefox152 {
		t.Fatalf("identity hints = %q, want first none, then the solve UA", got)
	}
	reqs := s.f.one.requests()
	if len(reqs) != 2 {
		t.Fatalf("doer calls = %d", len(reqs))
	}
	if reqs[0].header.Get("User-Agent") == uaFirefox152 {
		t.Fatal("first request must use our own UA")
	}
	h := reqs[1].header
	if h.Get("User-Agent") != uaFirefox152 || !hasClearance(&http.Request{Header: h}) {
		t.Fatalf("retry ua=%q cookie=%q, want solve UA with clearance", h.Get("User-Agent"), h.Get("Cookie"))
	}
	if _, ok := h["Sec-Ch-Ua"]; ok {
		t.Fatal("retry with Firefox UA must not carry Chrome client hints")
	}
}
