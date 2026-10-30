package client

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"ripperr/internal/helper/tlscapture"
	"ripperr/internal/helper/tlsclient"
	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/model/tracker"
)

const (
	uaLinux153     = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/153.0.0.0 Safari/537.36"
	clearanceValue = "cf_clearance=tok"
)

type flareStub struct {
	*identityProxy
	send    func(r *http.Request) (*http.Response, error)
	lastReq *http.Request
}

func (p *flareStub) Send(r *http.Request, opts *proxyModel.Options, pt proxyModel.ProxyType) (*http.Response, error) {
	p.mu.Lock()
	p.sends++
	p.lastOpts, p.lastType, p.lastReq = opts, pt, r
	p.mu.Unlock()
	return p.send(r)
}

func solveResponse(r *http.Request, ua string) *http.Response {
	res := okResponse(r, "solved")
	res.Header.Set("User-Agent", ua)
	res.Header.Add("Set-Cookie", clearanceValue+"; Path=/")
	return res
}

func headerChallenge(body string) *http.Response {
	res := okResponse(nil, body)
	res.StatusCode = http.StatusForbidden
	res.Header.Set("Cf-Mitigated", "challenge")
	return res
}

func hasClearance(req *http.Request) bool {
	return strings.Contains(req.Header.Get("Cookie"), clearanceValue)
}

type challengeSetup struct {
	f    *doerFactory
	px   *flareStub
	c    IClient
	hc   *http.Client
	sess *TLSSession
}

func newChallengeSetup(t *testing.T, ids ...tlscapture.Identity) *challengeSetup {
	t.Helper()
	if len(ids) == 0 {
		ids = []tlscapture.Identity{builtinIdentity()}
	}
	s := &challengeSetup{f: newDoerFactory(), hc: tlsJarClient(t)}
	s.sess = NewTLSSession(s.f.build)
	s.px = &flareStub{identityProxy: &identityProxy{ids: ids}}
	s.px.send = func(r *http.Request) (*http.Response, error) { return solveResponse(r, uaLinux152), nil }
	s.c = newTLSClient(s.hc, publicTLSTracker(), s.px, s.sess, nil)
	if err := s.c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTLSModeChallengeSolvedOnce(t *testing.T) {
	s := newChallengeSetup(t)
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
	if got := readAll(t, res); got != "page" {
		t.Fatalf("body = %q", got)
	}
	if s.px.sendCount() != 1 || s.px.lastReq.Method != http.MethodGet || s.px.lastReq.URL.String() != "https://tls.example/page" {
		t.Fatalf("sends=%d req=%+v", s.px.sendCount(), s.px.lastReq)
	}
	if o := s.px.lastOpts; !o.OnlyCookies || o.Session != "" || s.px.lastType != proxyModel.TypeFlare {
		t.Fatalf("opts=%+v type=%v", o, s.px.lastType)
	}
	reqs := s.f.one.requests()
	if len(reqs) != 2 {
		t.Fatalf("doer calls = %d", len(reqs))
	}
	if h := reqs[1].header; !strings.Contains(h.Get("Cookie"), clearanceValue) || h.Get("User-Agent") != uaLinux152 {
		t.Fatalf("retry headers: cookie=%q ua=%q", h.Get("Cookie"), h.Get("User-Agent"))
	}
	if n := len(s.f.configs()); n != 1 {
		t.Fatalf("builds = %d", n)
	}
}

func TestTLSModeServerRuleChallenge(t *testing.T) {
	s := newChallengeSetup(t)
	s.f.one.reply = func(_ int, req *http.Request) *http.Response {
		if hasClearance(req) {
			return nil
		}
		res := okResponse(nil, "<html><head><TITLE>Just a moment...</TITLE></head></html>")
		res.StatusCode = http.StatusServiceUnavailable
		res.Header.Set("Server", "cloudflare")
		return res
	}
	res, err := s.c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	if s.px.sendCount() != 1 {
		t.Fatalf("sends = %d", s.px.sendCount())
	}
}

func TestTLSModeHardBlockSkipsFlare(t *testing.T) {
	s := newChallengeSetup(t)
	s.f.one.reply = func(int, *http.Request) *http.Response {
		res := okResponse(nil, "<html><title>Access denied</title></html>")
		res.StatusCode = http.StatusForbidden
		res.Header.Set("Server", "cloudflare")
		return res
	}
	_, err := s.c.GetResponse(t.Context(), "https://tls.example/page", false)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusForbidden {
		t.Fatalf("err = %v", err)
	}
	if s.px.sendCount() != 0 {
		t.Fatalf("sends = %d", s.px.sendCount())
	}
}

func TestTLSModeChallengeAfterSolveErrors(t *testing.T) {
	s := newChallengeSetup(t)
	s.f.one.reply = func(int, *http.Request) *http.Response { return headerChallenge("blocked") }
	_, err := s.c.GetResponse(t.Context(), "https://tls.example/page", false)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusForbidden || !strings.Contains(err.Error(), "cloudflare challenge after flaresolverr solve") {
		t.Fatalf("err = %v", err)
	}
	if s.px.sendCount() != 1 || len(s.f.one.requests()) != 2 {
		t.Fatalf("sends=%d doer calls=%d", s.px.sendCount(), len(s.f.one.requests()))
	}
}

func TestTLSModeFlareErrorNotURLError(t *testing.T) {
	s := newChallengeSetup(t)
	s.f.one.reply = func(int, *http.Request) *http.Response { return headerChallenge("blocked") }
	s.px.send = func(*http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Post", URL: "http://flare:8191/v1", Err: errors.New("timeout")}
	}
	_, err := s.c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err == nil || !strings.Contains(err.Error(), "flaresolverr solve") {
		t.Fatalf("err = %v", err)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		t.Fatalf("url.Error leaked through: %v", err)
	}
	if len(s.f.one.requests()) != 1 {
		t.Fatalf("doer calls = %d, want no retry after a failed solve", len(s.f.one.requests()))
	}
}

func TestTLSModeConcurrentChallengesOneSolve(t *testing.T) {
	s := newChallengeSetup(t)
	// у брейкера клиента мьютекс на весь вызов, поэтому гонку дают два клиента одного трекера с общей сессией и jar
	second := newTLSClient(s.hc, publicTLSTracker(), s.px, s.sess, nil)
	if err := second.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	clients := []IClient{s.c, second}
	var arrived sync.WaitGroup
	arrived.Add(2)
	s.f.one.reply = func(n int, req *http.Request) *http.Response {
		if hasClearance(req) {
			return nil
		}
		arrived.Done()
		arrived.Wait()
		return headerChallenge("blocked")
	}
	var wg sync.WaitGroup
	errs := make([]error, len(clients))
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := clients[i].GetResponse(t.Context(), "https://tls.example/page", false)
			if err == nil {
				err = res.Body.Close()
			}
			errs[i] = err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.px.sendCount() != 1 {
		t.Fatalf("sends = %d", s.px.sendCount())
	}
}

func TestTLSModeSolveNewUAReResolves(t *testing.T) {
	id153 := tlscapture.Identity{UserAgent: uaLinux153, Profile: tlsclient.ChromeProfile(uaLinux153), Mode: tlscapture.ModeBuiltin}
	for _, tc := range []struct {
		name      string
		solutionU string
		wantCalls int
		wantUA    string
	}{
		{"new UA", uaLinux153, 2, uaLinux153},
		{"same UA", uaLinux152, 1, uaLinux152},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newChallengeSetup(t, builtinIdentity(), id153)
			s.px.send = func(r *http.Request) (*http.Response, error) { return solveResponse(r, tc.solutionU), nil }
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
			s.px.mu.Lock()
			calls := s.px.calls
			s.px.mu.Unlock()
			if calls != tc.wantCalls {
				t.Fatalf("ChromeIdentity calls = %d, want %d", calls, tc.wantCalls)
			}
			reqs := s.f.one.requests()
			if got := reqs[len(reqs)-1].header.Get("User-Agent"); got != tc.wantUA {
				t.Fatalf("retry UA = %q, want %q", got, tc.wantUA)
			}
		})
	}
}

func TestCloudflareChallengeRestoresBody(t *testing.T) {
	big := "<title>Access denied</title>" + strings.Repeat("x", 2*maxErrorResponse)
	res := okResponse(nil, big)
	res.StatusCode = http.StatusForbidden
	res.Header.Set("Server", "Cloudflare")
	challenged, err := cloudflareChallenge(res)
	if err != nil || challenged {
		t.Fatalf("challenged=%v err=%v", challenged, err)
	}
	if got := readAll(t, res); got != big {
		t.Fatalf("body not restored: %d bytes, want %d", len(got), len(big))
	}
}
