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

	"trackerr/internal/config"
	"trackerr/internal/helper"
	proxyModel "trackerr/internal/model/proxy"
	trackerModel "trackerr/internal/model/tracker"
	"trackerr/internal/tracker/client"
	"trackerr/internal/tracker/client/kinozal"
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

// TestLogin принимает config.App по значению, но Tracker это map: без своей копии
// разовый патч "проверить с этими данными" уходил бы в конфиг вызывающего
func TestTestLoginDoesNotMutateCallerConfig(t *testing.T) {
	cfg := config.App{Tracker: config.Tracker{
		"kinozal": &config.TrackerItem{Name: "kinozal", Domain: "kinozal.tv", Credentials: "real", Enabled: true},
	}}
	// enabled=false в патче даёт ранний выход до сети: до него ApplyTrackerKey уже отработал
	patch := json.RawMessage(`{"enabled":false,"credentials":"probe"}`)
	if err := TestLogin(context.Background(), cfg, nopLoginDB{}, "kinozal", patch); err == nil {
		t.Fatal("want disabled error, got nil")
	}
	if got := cfg.Tracker["kinozal"]; got.Credentials != "real" || !got.Enabled {
		t.Fatalf("caller config mutated: credentials=%q enabled=%v", got.Credentials, got.Enabled)
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
