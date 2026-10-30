package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	"ripperr/internal/helper/tlscapture"
	"ripperr/internal/helper/tlsclient"
	"ripperr/internal/model/proxy"
)

const (
	chrome146 = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	chrome103 = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/103.0.0.0 Safari/537.36"
)

var browserOrder = []string{
	"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests",
	"user-agent", "accept", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-user",
	"sec-fetch-dest", "accept-encoding", "accept-language", "priority",
}

type identityFlare struct {
	mu      sync.Mutex
	ua      string
	gets    int
	rootErr bool
	getErr  bool
	browser tls_client.HttpClient
}

func (s *identityFlare) setUA(ua string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ua = ua
}

func (s *identityFlare) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

func (s *identityFlare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	s.mu.Lock()
	ua, rootErr, getErr := s.ua, s.rootErr, s.getErr
	s.mu.Unlock()
	if r.Method == http.MethodGet {
		if rootErr {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"userAgent": ua})
		return
	}
	var p struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(r.Body).Decode(&p)
	s.mu.Lock()
	s.gets++
	s.mu.Unlock()
	if getErr {
		_, _ = w.Write([]byte(`{"status":"error","message":"boom"}`))
		return
	}
	req, err := fhttp.NewRequest(fhttp.MethodGet, p.URL, nil)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	req.Header = fhttp.Header{
		"sec-ch-ua":                 {`"Chromium";v="146", "Not-A.Brand";v="24", "Google Chrome";v="146"`},
		"sec-ch-ua-mobile":          {"?0"},
		"sec-ch-ua-platform":        {`"Linux"`},
		"upgrade-insecure-requests": {"1"},
		"user-agent":                {chrome146},
		"accept":                    {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		"sec-fetch-site":            {"none"},
		"sec-fetch-mode":            {"navigate"},
		"sec-fetch-user":            {"?1"},
		"sec-fetch-dest":            {"document"},
		"accept-encoding":           {"gzip, deflate, br, zstd"},
		"accept-language":           {"en-US,en;q=0.9"},
		"priority":                  {"u=0, i"},
		fhttp.HeaderOrderKey:        browserOrder,
	}
	resp, err := s.browser.Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"solution": map[string]any{"status": 200, "response": "ok", "userAgent": ua},
	})
}

func newIdentityProxy(t *testing.T, ua string) (*identityFlare, *client) {
	t.Helper()
	browser, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithClientProfile(profiles.Chrome_146),
		tls_client.WithInsecureSkipVerify(),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithTimeoutSeconds(10),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(browser.CloseIdleConnections)
	s := &identityFlare{ua: ua, browser: browser}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	px := New(srv.Client(), &proxy.Config{Flaresolverr: proxy.ItemConfig{URL: srv.URL}}).(*client)
	return s, px
}

func TestChromeIdentityBuiltinMatch(t *testing.T) {
	s, px := newIdentityProxy(t, chrome146)
	id, err := px.ChromeIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.Mode != tlscapture.ModeBuiltin || id.Profile != "chrome_146" {
		t.Fatalf("mode %d profile %q, want builtin chrome_146", id.Mode, id.Profile)
	}
	if id.Fingerprint == nil || id.Fingerprint.Headers["user-agent"] == "" {
		t.Fatalf("no captured user-agent: %+v", id.Fingerprint)
	}
	if n := s.count(); n != 1 {
		t.Fatalf("request.get count %d, want 1", n)
	}
}

func TestChromeIdentityCachedPerUA(t *testing.T) {
	s, px := newIdentityProxy(t, chrome146)
	if _, err := px.ChromeIdentity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := px.ChromeIdentity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := s.count(); n != 1 {
		t.Fatalf("request.get count %d after second call, want 1", n)
	}
	s.setUA(strings.Replace(chrome146, "Chrome/146", "Chrome/145", 1))
	if _, err := px.ChromeIdentity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := s.count(); n != 2 {
		t.Fatalf("request.get count %d after UA change, want 2", n)
	}
}

func TestChromeIdentityCustomWhenBuiltinDiffers(t *testing.T) {
	_, px := newIdentityProxy(t, chrome103)
	id, err := px.ChromeIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.Mode != tlscapture.ModeCustom || id.Fingerprint == nil {
		t.Fatalf("mode %d fingerprint %v, want custom with fingerprint", id.Mode, id.Fingerprint)
	}
}

func TestChromeIdentityFlareOnly(t *testing.T) {
	s, px := newIdentityProxy(t, chrome146)
	px.probe = func(ctx context.Context, cfg tlsclient.Config, target string) error {
		cfg.Profile = "chrome_103"
		cfg.Fingerprint = nil
		return tlsclient.Probe(ctx, cfg, target)
	}
	id, err := px.ChromeIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.Mode != tlscapture.ModeFlare {
		t.Fatalf("mode %d, want flare", id.Mode)
	}
	if n := s.count(); n != 1 {
		t.Fatalf("request.get count %d, want 1", n)
	}
}

func TestChromeIdentityCaptureFailureFallsBack(t *testing.T) {
	s, px := newIdentityProxy(t, chrome146)
	s.getErr = true
	for range 2 {
		id, err := px.ChromeIdentity(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if id.Mode != tlscapture.ModeBuiltin || id.Fingerprint != nil || id.Profile != tlsclient.ChromeProfile(chrome146) {
			t.Fatalf("fallback identity %+v", id)
		}
		if id.UserAgent != chrome146 {
			t.Fatalf("user agent %q", id.UserAgent)
		}
	}
	if n := s.count(); n != 1 {
		t.Fatalf("request.get count %d, want 1", n)
	}
}

func TestChromeIdentityUAProbeError(t *testing.T) {
	s, px := newIdentityProxy(t, chrome146)
	s.rootErr = true
	if _, err := px.ChromeIdentity(context.Background()); err == nil {
		t.Fatal("want error when GET / fails")
	}
}

func TestChromeIdentitySlotHeldNoFailureCached(t *testing.T) {
	s, px := newIdentityProxy(t, chrome146)
	flareSlots <- struct{}{}
	released := false
	release := func() {
		if !released {
			released = true
			<-flareSlots
		}
	}
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	id, err := px.ChromeIdentity(ctx)
	if err == nil && id.Fingerprint != nil {
		t.Fatalf("unexpected capture while the slot is held: %+v", id)
	}
	if n := s.count(); n != 0 {
		t.Fatalf("request.get count %d while the slot is held", n)
	}
	release()
	id, err = px.ChromeIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id.Fingerprint == nil {
		t.Fatal("failure was cached: no fingerprint after the slot was released")
	}
	if n := s.count(); n != 1 {
		t.Fatalf("request.get count %d, want 1", n)
	}
}

func TestReceiverOptions(t *testing.T) {
	got, err := receiverOptions(proxy.ItemConfig{URL: "http://127.0.0.1:8191/v1", CaptureHost: "10.0.0.5", CapturePort: 6411})
	if err != nil {
		t.Fatal(err)
	}
	if got.ListenAddr != ":6411" || got.AdvertiseHost != "10.0.0.5" {
		t.Fatalf("explicit: %+v", got)
	}
	got, err = receiverOptions(proxy.ItemConfig{URL: "http://127.0.0.1:8191/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ListenAddr != "127.0.0.1:0" || got.AdvertiseHost != "127.0.0.1" {
		t.Fatalf("auto: %+v", got)
	}
}
