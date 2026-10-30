package tracker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/helper"
	"ripperr/internal/helper/tlsclient"
	proxyModel "ripperr/internal/model/proxy"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/client"
	"ripperr/internal/tracker/client/kinozal"
)

type stubFlareProxy struct{}

func (stubFlareProxy) Send(r *http.Request, _ *proxyModel.Options, pt proxyModel.ProxyType) (*http.Response, error) {
	if pt != proxyModel.TypeFlare {
		return nil, io.EOF
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("<html>Вход</html>")),
		Request:    r,
		Header:     http.Header{},
	}, nil
}

func (stubFlareProxy) DestroyFlareSession(context.Context, string) error { return nil }

func TestInitClientKinozalRejectsMissingAuthCookies(t *testing.T) {
	conf := trackerModel.Tracker{
		Name:         "kinozal",
		Domain:       "kinozal.tv",
		ActiveDomain: "kinozal.guru",
		ProxyType:    proxyModel.TypeFlare,
		Publicity:    "private",
		LoginURL:     "https://kinozal.tv/takelogin.php",
		Credentials:  "login=x&login_password=y",
		Enabled:      true,
	}
	tr := withActiveDomain(kinozal.New(conf), "kinozal.guru")

	cj, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Jar: cj, Timeout: 5 * time.Second}
	tc := client.New(hc, hc, tr, stubFlareProxy{}, helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{}))

	err = tc.InitClient(context.Background(), &trackerModel.Tracker{
		TypeID:      KinozalID,
		Publicity:   "private",
		Credentials: conf.Credentials,
		Exists:      true,
	}, true)
	if err == nil {
		t.Fatal("expected login auth error")
	}
	if !strings.Contains(err.Error(), "login failed: no auth cookies (uid/pass)") {
		t.Fatalf("err = %q", err.Error())
	}
	if !strings.Contains(err.Error(), "username=...&password=...") {
		t.Fatalf("expected credentials hint, err = %q", err.Error())
	}
}

func TestInitClientKinozalForceLoginSkipsDBCookieFallback(t *testing.T) {
	conf := trackerModel.Tracker{
		Name:        "kinozal",
		Domain:      "kinozal.guru",
		ProxyType:   proxyModel.TypeFlare,
		Publicity:   "private",
		LoginURL:    "https://kinozal.guru/takelogin.php",
		Credentials: "login=x&login_password=y",
		Enabled:     true,
	}
	tr := kinozal.New(conf)
	hc := &http.Client{Timeout: 5 * time.Second}
	tc := client.New(hc, hc, tr, stubFlareProxy{}, helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{}))

	stale := []*http.Cookie{{Name: "uid", Value: "old"}, {Name: "pass", Value: "old"}}
	err := tc.InitClient(context.Background(), &trackerModel.Tracker{
		TypeID:      KinozalID,
		Publicity:   "private",
		Credentials: conf.Credentials,
		Cookies:     stale,
		Exists:      true,
	}, true)
	if err == nil {
		t.Fatal("expected error when flare login returns no fresh auth cookies")
	}
}

func TestLoginAuthHintOnlyKinozal(t *testing.T) {
	tr := kinozal.New(trackerModel.Tracker{})
	if tr.LoginAuthHint() == "" {
		t.Fatal("expected kinozal credentials hint")
	}
}

type nopLoginDB struct{}

func (nopLoginDB) GetTracker(context.Context, int) (*trackerModel.Tracker, error) {
	return &trackerModel.Tracker{}, nil
}

func TestTestLoginDoesNotMutateCallerConfig(t *testing.T) {
	cfg := config.App{Runner: config.Runner{TLSProfile: "chrome_131"}, Tracker: config.Tracker{
		"rutor": &config.TrackerItem{Name: "rutor", Domain: "rutor.info", Publicity: "public", Credentials: "real", Enabled: true},
	}}
	// публичный трекер выходит из InitClient до сети, но патч к копии уже применен
	patch := json.RawMessage(`{"enabled":false,"credentials":"probe"}`)
	if err := TestLogin(context.Background(), cfg, nopLoginDB{}, "rutor", patch); err != nil {
		t.Fatalf("TestLogin: %v", err)
	}
	if got := cfg.Tracker["rutor"]; got.Credentials != "real" || !got.Enabled {
		t.Fatalf("caller config mutated: credentials=%q enabled=%v", got.Credentials, got.Enabled)
	}
}

func TestTestLoginRunsForDisabledTracker(t *testing.T) {
	cfg := config.App{Runner: config.Runner{TLSProfile: "chrome_131"}, Tracker: config.Tracker{
		"rutor": &config.TrackerItem{Name: "rutor", Domain: "rutor.info", Publicity: "public", Enabled: false},
	}}
	if err := TestLogin(context.Background(), cfg, nopLoginDB{}, "rutor", nil); err != nil {
		t.Fatalf("disabled tracker must be testable: %v", err)
	}
	patch := json.RawMessage(`{"enabled":false}`)
	if err := TestLogin(context.Background(), cfg, nopLoginDB{}, "rutor", patch); err != nil {
		t.Fatalf("patch enabled=false: %v", err)
	}
	if cfg.Tracker["rutor"].Enabled {
		t.Fatal("caller config must stay disabled")
	}
}

func TestTrackerIDsResolved(t *testing.T) {
	ids := map[string]int{"rutracker": RutrackerID, "kinozal": KinozalID, "rutor": RutorID}
	seen := make(map[int]string, len(ids))
	for key, id := range ids {
		if id == 0 {
			t.Fatalf("%s id = 0: реестр пуст, blank-импорт registry/all потерялся", key)
		}
		if prev, dup := seen[id]; dup {
			t.Fatalf("%s и %s делят id %d", prev, key, id)
		}
		seen[id] = key
	}
}

type flarePayload struct {
	Cmd     string `json:"cmd"`
	Session string `json:"session"`
}

type loginPageDoer struct {
	mu      sync.Mutex
	urls    []string
	cookies []string
	setCF   string
}

func (d *loginPageDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.urls = append(d.urls, req.URL.String())
	d.cookies = append(d.cookies, req.Header.Get("Cookie"))
	d.mu.Unlock()
	res := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("<html>ok</html>")), Request: req}
	res.Header.Add("Set-Cookie", "bb_session=0-1-abc; Path=/")
	if d.setCF != "" {
		res.Header.Add("Set-Cookie", "cf_clearance="+d.setCF+"; Path=/")
	}
	return res, nil
}

type offlineLogin struct {
	mu       sync.Mutex
	payloads []flarePayload
	pages    *loginPageDoer
}

func runOfflineRutrackerLogin(t *testing.T) *offlineLogin {
	t.Helper()
	return runOfflineRutrackerLoginWith(t, &loginPageDoer{})
}

func runOfflineRutrackerLoginWith(t *testing.T, pages *loginPageDoer) *offlineLogin {
	t.Helper()
	o := &offlineLogin{pages: pages}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			_, _ = io.WriteString(w, `{"msg":"FlareSolverr is ready!","userAgent":"ua"}`)
			return
		}
		var p flarePayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		o.mu.Lock()
		o.payloads = append(o.payloads, p)
		o.mu.Unlock()
		if p.Cmd == "sessions.destroy" {
			_, _ = io.WriteString(w, `{"status":"ok"}`)
			return
		}
		_, _ = io.WriteString(w, `{"status":"ok","solution":{"status":200,"response":"<html>ok</html>","userAgent":"ua"}}`)
	}))
	t.Cleanup(srv.Close)
	loginTLSDoer = func(tlsclient.Config) (helper.HTTPDoer, error) { return o.pages, nil }
	t.Cleanup(func() { loginTLSDoer = nil })

	cfg := config.App{
		Runner:       config.Runner{TLSProfile: "chrome_131"},
		Flaresolverr: config.Flaresolverr{Host: srv.URL, MaxTimeout: 1000},
		Tracker: config.Tracker{
			"rutracker": &config.TrackerItem{
				Name: "rutracker", Domain: "rutracker.org", ActiveDomain: "rutracker.org",
				Publicity: "private", ProxyType: int(proxyModel.TypeFlare),
				LoginURL: "https://rutracker.org/forum/login.php", Credentials: "login_username=a&login_password=b",
				Enabled: true,
			},
		},
	}
	if err := TestLogin(context.Background(), cfg, nopLoginDB{}, "rutracker", nil); err != nil {
		t.Fatalf("TestLogin: %v", err)
	}
	return o
}

func TestTestLoginUsesOwnFlareSession(t *testing.T) {
	o := runOfflineRutrackerLogin(t)
	o.pages.mu.Lock()
	if len(o.pages.urls) == 0 {
		t.Fatal("login page did not go through the direct tls doer")
	}
	o.pages.mu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.payloads) == 0 {
		t.Fatal("test flare session was not destroyed")
	}
	var testSession string
	for _, p := range o.payloads {
		if p.Session == "" {
			continue
		}
		if !strings.HasPrefix(p.Session, "rutracker-test-") {
			t.Fatalf("session = %q, want rutracker-test- prefix: %+v", p.Session, p)
		}
		testSession = p.Session
	}
	last := o.payloads[len(o.payloads)-1]
	if last.Cmd != "sessions.destroy" || last.Session != testSession {
		t.Fatalf("last payload = %+v, want destroy of %q", last, testSession)
	}
}

func TestTestLoginReusesLiveCloudflareClearance(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://rutracker.org/forum/")
	jar.SetCookies(u, []*http.Cookie{
		{Name: "cf_clearance", Value: "live-cf", Path: "/"},
		{Name: "bb_session", Value: "live-session", Path: "/"},
	})
	setLiveJar(jar)
	t.Cleanup(func() { setLiveJar(nil) })

	o := runOfflineRutrackerLogin(t)
	o.pages.mu.Lock()
	defer o.pages.mu.Unlock()
	if len(o.pages.cookies) == 0 || !strings.Contains(o.pages.cookies[0], "cf_clearance=live-cf") {
		t.Fatalf("first request cookies = %q, want live cf_clearance", o.pages.cookies)
	}
	for _, c := range o.pages.cookies {
		if strings.Contains(c, "live-session") {
			t.Fatalf("live login cookie leaked into the test: %q", c)
		}
	}
}

func TestTestLoginSharesFreshClearanceWithLiveJar(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://rutracker.org/forum/")
	jar.SetCookies(u, []*http.Cookie{
		{Name: "cf_clearance", Value: "stale-cf", Path: "/"},
		{Name: "bb_session", Value: "live-session", Path: "/"},
	})
	setLiveJar(jar)
	t.Cleanup(func() { setLiveJar(nil) })

	runOfflineRutrackerLoginWith(t, &loginPageDoer{setCF: "fresh-cf"})
	got := helper.CookiesString(jar.Cookies(u))
	if !strings.Contains(got, "cf_clearance=fresh-cf") || strings.Contains(got, "stale-cf") {
		t.Fatalf("live jar = %q, want fresh cf_clearance from the test", got)
	}
	if !strings.Contains(got, "bb_session=live-session") {
		t.Fatalf("live login cookie replaced by the test: %q", got)
	}
}
