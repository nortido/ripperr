package client

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"slices"
	"strings"
	"sync"
	"testing"

	"ripperr/internal/helper"
	"ripperr/internal/helper/tlscapture"
	"ripperr/internal/helper/tlsclient"
	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/model/tracker"
	"ripperr/internal/proxy"
)

const (
	uaLinux152 = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"
	secChUA152 = `"Chromium";v="152", "Not)A;Brand";v="24"`
)

type seenRequest struct {
	url           string
	method        string
	header        http.Header
	contentLength int64
}

type recDoer struct {
	mu    sync.Mutex
	seen  []seenRequest
	reply func(n int, req *http.Request) *http.Response
}

func (d *recDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.seen = append(d.seen, seenRequest{url: req.URL.String(), method: req.Method, header: req.Header.Clone(), contentLength: req.ContentLength})
	n := len(d.seen)
	d.mu.Unlock()
	if d.reply != nil {
		if res := d.reply(n, req); res != nil {
			res.Request = req
			return res, nil
		}
	}
	return okResponse(req, "page"), nil
}

func (d *recDoer) requests() []seenRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.seen)
}

func okResponse(req *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

type doerFactory struct {
	mu   sync.Mutex
	cfgs []tlsclient.Config
	one  *recDoer
}

func newDoerFactory() *doerFactory { return &doerFactory{one: &recDoer{}} }

func (f *doerFactory) build(cfg tlsclient.Config) (helper.HTTPDoer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfgs = append(f.cfgs, cfg)
	return f.one, nil
}

func (f *doerFactory) configs() []tlsclient.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cfgs)
}

type identityProxy struct {
	mu       sync.Mutex
	ids      []tlscapture.Identity
	calls    int
	sends    int
	destroys int
	lastOpts *proxyModel.Options
	lastType proxyModel.ProxyType
}

func (p *identityProxy) Send(r *http.Request, opts *proxyModel.Options, pt proxyModel.ProxyType) (*http.Response, error) {
	p.mu.Lock()
	p.sends++
	p.lastOpts = opts
	p.lastType = pt
	p.mu.Unlock()
	return okResponse(r, "flare"), nil
}

func (p *identityProxy) DestroyFlareSession(context.Context, string) error {
	p.mu.Lock()
	p.destroys++
	p.mu.Unlock()
	return nil
}

func (p *identityProxy) ChromeIdentity(context.Context) (tlscapture.Identity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := min(p.calls, len(p.ids)-1)
	p.calls++
	return p.ids[i], nil
}

func (p *identityProxy) sendCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sends
}

func builtinIdentity() tlscapture.Identity {
	return tlscapture.Identity{UserAgent: uaLinux152, Profile: tlsclient.ChromeProfile(uaLinux152), Mode: tlscapture.ModeBuiltin}
}

func capturedFingerprint() *tlscapture.Fingerprint {
	return &tlscapture.Fingerprint{
		Headers: map[string]string{
			"user-agent":         uaLinux152,
			"sec-ch-ua":          secChUA152,
			"sec-ch-ua-platform": `"Linux"`,
			"accept-language":    "en-US,en;q=0.9",
			"accept-encoding":    "gzip, deflate, br, zstd",
			"priority":           "u=0, i",
		},
		HeaderOrder: []string{"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests", "user-agent", "accept", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "accept-encoding", "accept-language", "priority"},
	}
}

func tlsJarClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Errorf("page request went through hc: %s", req.URL)
		return okResponse(req, ""), nil
	})}
}

func publicTLSTracker() *tracker.Tracker {
	return &tracker.Tracker{Name: "tlspub", Domain: "tls.example", ProxyType: proxyModel.TypeTLS, Publicity: tracker.PublicityPublic}
}

func newTLSClient(hc *http.Client, tr ITracker, px proxy.IClient, sess *TLSSession, dl helper.HTTPDoer) IClient {
	return New(hc, dl, tr, px, helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{}), WithTLSSession(sess))
}

func readAll(t *testing.T, res *http.Response) string {
	t.Helper()
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTLSModeDirectNoFlare(t *testing.T) {
	f := newDoerFactory()
	px := &identityProxy{ids: []tlscapture.Identity{builtinIdentity()}}
	c := newTLSClient(tlsJarClient(t), publicTLSTracker(), px, NewTLSSession(f.build), nil)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, res); got != "page" {
		t.Fatalf("body = %q", got)
	}
	cfgs := f.configs()
	if px.sendCount() != 0 || len(cfgs) != 1 || len(f.one.requests()) != 1 {
		t.Fatalf("sends=%d builds=%d requests=%d", px.sendCount(), len(cfgs), len(f.one.requests()))
	}
	if !cfgs[0].Direct || !cfgs[0].NoRedirects {
		t.Fatalf("page doer must be direct without redirects: %+v", cfgs[0])
	}
}

func TestTLSModePagesUseTrackerProxy(t *testing.T) {
	const socks = "socks5://127.0.0.1:12808"
	f := newDoerFactory()
	px := &identityProxy{ids: []tlscapture.Identity{builtinIdentity()}}
	hc := tlsJarClient(t)
	hc.Transport = helper.ProxyTransport(socks)
	c := newTLSClient(hc, publicTLSTracker(), px, NewTLSSession(f.build), nil)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	cfgs := f.configs()
	if len(cfgs) != 1 || cfgs[0].ProxyURL != socks {
		t.Fatalf("page doer must go through the tracker proxy %s: %+v", socks, cfgs)
	}
}

func TestTLSModeReusesDoerAndCookies(t *testing.T) {
	f := newDoerFactory()
	f.one.reply = func(n int, _ *http.Request) *http.Response {
		if n != 1 {
			return nil
		}
		res := okResponse(nil, "one")
		res.Header.Add("Set-Cookie", "a=1; Path=/")
		return res
	}
	px := &identityProxy{ids: []tlscapture.Identity{builtinIdentity()}}
	c := newTLSClient(tlsJarClient(t), publicTLSTracker(), px, NewTLSSession(f.build), nil)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
		if err != nil {
			t.Fatal(err)
		}
		readAll(t, res)
	}
	reqs := f.one.requests()
	if len(f.configs()) != 1 || len(reqs) != 2 {
		t.Fatalf("builds=%d requests=%d", len(f.configs()), len(reqs))
	}
	if got := reqs[1].header.Get("Cookie"); !strings.Contains(got, "a=1") {
		t.Fatalf("second Cookie = %q", got)
	}
}

func TestTLSModeSessionSurvivesClientRebuild(t *testing.T) {
	f := newDoerFactory()
	f.one.reply = func(n int, _ *http.Request) *http.Response {
		if n != 1 {
			return nil
		}
		res := okResponse(nil, "one")
		res.Header.Add("Set-Cookie", "b=2; Path=/")
		return res
	}
	px := &identityProxy{ids: []tlscapture.Identity{builtinIdentity()}}
	sess := NewTLSSession(f.build)
	hc := tlsJarClient(t)
	for range 2 {
		c := newTLSClient(hc, publicTLSTracker(), px, sess, nil)
		if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
			t.Fatal(err)
		}
		res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
		if err != nil {
			t.Fatal(err)
		}
		readAll(t, res)
	}
	reqs := f.one.requests()
	if len(f.configs()) != 1 || len(reqs) != 2 {
		t.Fatalf("builds=%d requests=%d", len(f.configs()), len(reqs))
	}
	if got := reqs[1].header.Get("Cookie"); !strings.Contains(got, "b=2") {
		t.Fatalf("client 2 Cookie = %q", got)
	}
}

func TestTLSModeIdentityChangeRebuildsDoer(t *testing.T) {
	f := newDoerFactory()
	fp1, fp2 := capturedFingerprint(), capturedFingerprint()
	id1, id2 := builtinIdentity(), builtinIdentity()
	id1.Mode, id1.Fingerprint = tlscapture.ModeCustom, fp1
	id2.Mode, id2.Fingerprint = tlscapture.ModeCustom, fp2
	px := &identityProxy{ids: []tlscapture.Identity{id1, id2}}
	sess := NewTLSSession(f.build)
	hc := tlsJarClient(t)
	for range 2 {
		c := newTLSClient(hc, publicTLSTracker(), px, sess, nil)
		if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
			t.Fatal(err)
		}
	}
	cfgs := f.configs()
	if len(cfgs) != 2 || cfgs[0].Fingerprint != fp1 || cfgs[1].Fingerprint != fp2 {
		t.Fatalf("configs = %+v", cfgs)
	}
}

func TestTLSModeCapturedHeaders(t *testing.T) {
	f := newDoerFactory()
	id := builtinIdentity()
	id.Fingerprint = capturedFingerprint()
	px := &identityProxy{ids: []tlscapture.Identity{id}}
	c := newTLSClient(tlsJarClient(t), publicTLSTracker(), px, NewTLSSession(f.build), nil)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	staleUA := c.(*client).header.Get("User-Agent")
	res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	h := f.one.requests()[0].header
	want := map[string]string{
		"User-Agent":         uaLinux152,
		"Sec-Ch-Ua":          secChUA152,
		"Sec-Ch-Ua-Platform": `"Linux"`,
		"Accept-Language":    "en-US,en;q=0.9",
		"Accept-Encoding":    "gzip, deflate, br, zstd",
		"Priority":           "u=0, i",
		"Sec-Fetch-Site":     "same-origin",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
	if !strings.Contains(h.Get("Sec-Ch-Ua"), `"Chromium";v="152"`) || !strings.Contains(h.Get("User-Agent"), "Chrome/152") {
		t.Fatalf("UA and sec-ch-ua majors differ: %q %q", h.Get("User-Agent"), h.Get("Sec-Ch-Ua"))
	}
	cfg := f.configs()[0]
	if !slices.Equal(cfg.HeaderOrder, mergeHeaderOrder(id.Fingerprint.HeaderOrder, chromeHeaderOrder)) {
		t.Fatalf("HeaderOrder = %v", cfg.HeaderOrder)
	}
	if cfg.Profile != id.Profile || cfg.Fingerprint != nil {
		t.Fatalf("profile=%q fingerprint=%v", cfg.Profile, cfg.Fingerprint)
	}
	if got := c.(*client).header.Get("User-Agent"); got != staleUA || !strings.Contains(got, "Chrome/140") {
		t.Fatalf("c.header was mutated: %q", got)
	}
}

func TestTLSModeCustomProfile(t *testing.T) {
	f := newDoerFactory()
	id := builtinIdentity()
	id.Mode, id.Fingerprint = tlscapture.ModeCustom, capturedFingerprint()
	px := &identityProxy{ids: []tlscapture.Identity{id}}
	c := newTLSClient(tlsJarClient(t), publicTLSTracker(), px, NewTLSSession(f.build), nil)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	cfgs := f.configs()
	if len(cfgs) != 1 || cfgs[0].Fingerprint != id.Fingerprint || cfgs[0].Profile != id.Profile {
		t.Fatalf("configs = %+v", cfgs)
	}
}

func TestTLSModeFallbackSecChUA(t *testing.T) {
	f := newDoerFactory()
	id := builtinIdentity()
	px := &identityProxy{ids: []tlscapture.Identity{id}}
	c := newTLSClient(tlsJarClient(t), publicTLSTracker(), px, NewTLSSession(f.build), nil)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	h := f.one.requests()[0].header
	if got := h.Get("Sec-Ch-Ua"); !strings.Contains(got, `v="152"`) || strings.Contains(got, `v="140"`) {
		t.Fatalf("sec-ch-ua = %q", got)
	}
	if got := h.Get("Sec-Ch-Ua-Platform"); got != `"Linux"` {
		t.Fatalf("platform = %q", got)
	}
	if got := h.Get("User-Agent"); got != uaLinux152 {
		t.Fatalf("user-agent = %q", got)
	}
	cfg := f.configs()[0]
	if cfg.Profile != "chrome_152" || !slices.Equal(cfg.HeaderOrder, chromeHeaderOrder) {
		t.Fatalf("profile=%q order=%v", cfg.Profile, cfg.HeaderOrder)
	}
}

func TestTLSModeNoIdentityProvider(t *testing.T) {
	f := newDoerFactory()
	px := &countingProxy{}
	c := newTLSClient(tlsJarClient(t), publicTLSTracker(), px, NewTLSSession(f.build), nil)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	h := f.one.requests()[0].header
	if ua := h.Get("User-Agent"); !strings.Contains(ua, "Chrome/140") {
		t.Fatalf("user-agent = %q", ua)
	}
	if got := h.Get("Sec-Ch-Ua"); !strings.Contains(got, `v="140"`) {
		t.Fatalf("sec-ch-ua = %q", got)
	}
	if want := tlsclient.ChromeProfile(h.Get("User-Agent")); f.configs()[0].Profile != want || want == "" {
		t.Fatalf("profile = %q, want %q", f.configs()[0].Profile, want)
	}
	if px.sends != 0 {
		t.Fatalf("sends = %d", px.sends)
	}
}

func TestTLSModeFlareOnly(t *testing.T) {
	flareID := builtinIdentity()
	flareID.Mode = tlscapture.ModeFlare
	f := newDoerFactory()
	px := &identityProxy{ids: []tlscapture.Identity{flareID}}
	tr := &flareOptsTracker{Tracker: *publicTLSTracker(), session: "tlsflare"}
	c := newTLSClient(tlsJarClient(t), tr, px, NewTLSSession(f.build), nil)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	res, err := c.GetResponse(t.Context(), "https://tls.example/page", false)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	if px.sends != 1 || px.lastType != proxyModel.TypeFlare || px.lastOpts == nil || px.lastOpts.Session != "tlsflare" {
		t.Fatalf("sends=%d type=%d opts=%+v", px.sends, px.lastType, px.lastOpts)
	}
	if len(f.configs()) != 0 {
		t.Fatalf("builds = %d", len(f.configs()))
	}
	if !c.(interface{ EncodedBody() bool }).EncodedBody() {
		t.Fatal("flare-only EncodedBody must be true")
	}

	direct := newTLSClient(tlsJarClient(t), publicTLSTracker(), &identityProxy{ids: []tlscapture.Identity{builtinIdentity()}}, NewTLSSession(newDoerFactory().build), nil)
	if err := direct.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	if direct.(interface{ EncodedBody() bool }).EncodedBody() {
		t.Fatal("builtin EncodedBody must be false")
	}
}

func TestTLSModeSessionTrackerLogsInDirect(t *testing.T) {
	f := newDoerFactory()
	f.one.reply = func(_ int, _ *http.Request) *http.Response {
		res := okResponse(nil, "login")
		res.Header.Add("Set-Cookie", "bb_session=abc; Path=/")
		return res
	}
	px := &identityProxy{ids: []tlscapture.Identity{builtinIdentity()}}
	tr := &flareOptsTracker{
		Tracker: tracker.Tracker{
			Name: "tlslogin", Domain: "tls.example", ProxyType: proxyModel.TypeTLS, Publicity: "private",
			LoginURL: "https://tls.example/login.php", Credentials: "u=1&p=2",
		},
		session: "tlslogin",
	}
	c := newTLSClient(tlsJarClient(t), tr, px, NewTLSSession(f.build), nil)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, true); err != nil {
		t.Fatal(err)
	}
	if px.destroys != 0 || px.sendCount() != 0 {
		t.Fatalf("destroys=%d sends=%d", px.destroys, px.sendCount())
	}
	reqs := f.one.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodPost || reqs[0].url != "https://tls.example/login.php" {
		t.Fatalf("requests = %+v", reqs)
	}
	if _, ok := reqs[0].header["Host"]; ok {
		t.Fatal("Host key must be dropped")
	}
	if _, ok := reqs[0].header["Content-Length"]; ok {
		t.Fatal("Content-Length key must be dropped")
	}
	if reqs[0].contentLength != int64(len("u=1&p=2")) {
		t.Fatalf("ContentLength = %d", reqs[0].contentLength)
	}
	if got := helper.CookiesString(c.Cookies()); !strings.Contains(got, "bb_session=abc") {
		t.Fatalf("cookies = %q", got)
	}
}

func TestTLSModeDownloadHeaders(t *testing.T) {
	f := newDoerFactory()
	dl := &recDoer{}
	id := builtinIdentity()
	id.Fingerprint = capturedFingerprint()
	px := &identityProxy{ids: []tlscapture.Identity{id}}
	c := newTLSClient(tlsJarClient(t), publicTLSTracker(), px, NewTLSSession(f.build), dl)
	if err := c.InitClient(t.Context(), &tracker.Tracker{}, false); err != nil {
		t.Fatal(err)
	}
	res, err := c.GetResponse(t.Context(), "https://tls.example/dl?t=1", true)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, res)
	reqs := dl.requests()
	if len(reqs) != 1 || len(f.one.requests()) != 0 {
		t.Fatalf("download requests=%d page requests=%d", len(reqs), len(f.one.requests()))
	}
	want := map[string]string{
		"User-Agent":         uaLinux152,
		"Sec-Ch-Ua":          secChUA152,
		"Sec-Ch-Ua-Mobile":   "?0",
		"Sec-Ch-Ua-Platform": `"Linux"`,
		"Accept-Language":    "en-US,en;q=0.9",
	}
	for k, v := range want {
		if got := reqs[0].header.Get(k); got != v {
			t.Fatalf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestMergeHeaderOrder(t *testing.T) {
	captured := []string{"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests", "user-agent", "accept", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-user", "sec-fetch-dest", "accept-encoding", "accept-language", "priority"}
	if got := mergeHeaderOrder(captured, chromeHeaderOrder); !slices.Equal(got, chromeHeaderOrder) {
		t.Fatalf("merged = %v", got)
	}
	if got := mergeHeaderOrder(nil, chromeHeaderOrder); !slices.Equal(got, chromeHeaderOrder) {
		t.Fatalf("empty captured = %v", got)
	}
}
