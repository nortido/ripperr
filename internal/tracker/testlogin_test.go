package tracker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/helper"
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

func (stubFlareProxy) DestroyFlareSession(string) error { return nil }

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
