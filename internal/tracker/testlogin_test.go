package tracker

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

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
