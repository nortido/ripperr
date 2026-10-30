package tracker

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/helper/tlscapture"
	"ripperr/internal/helper/tlsclient"
	proxyModel "ripperr/internal/model/proxy"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/client"
	"ripperr/internal/tracker/client/rutracker"
)

const uaTLSLinux = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"

type identityRecordingProxy struct {
	recordingProxy
}

func (p *identityRecordingProxy) ChromeIdentity(context.Context) (tlscapture.Identity, error) {
	return tlscapture.Identity{UserAgent: uaTLSLinux, Profile: tlsclient.ChromeProfile(uaTLSLinux), Mode: tlscapture.ModeBuiltin}, nil
}

type countingTLSDoer struct {
	mu    sync.Mutex
	calls int
}

func (d *countingTLSDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("<html>ok</html>")), Request: req}, nil
}

func (d *countingTLSDoer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func TestTLSRebuildReusesSession(t *testing.T) {
	tr := rutracker.New(trackerModel.Tracker{
		Name:        "rutracker",
		Domain:      "rutracker.org",
		ProxyType:   proxyModel.TypeTLS,
		Publicity:   "private",
		LoginURL:    "https://rutracker.org/forum/login.php",
		Credentials: "login_username=x&login_password=y",
		Enabled:     true,
	})
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	px := &identityRecordingProxy{}
	s := newBuildCacheService(map[int]client.ITracker{RutrackerID: tr})
	s.hc = &http.Client{Timeout: 5 * time.Second, Jar: jar}
	s.px = px
	s.db = &savedCookiesDB{creds: tr.GetCredentials()}
	s.cbCfg = &helper.CircuitBreakerConfig{MaxFailures: 100, Timeout: time.Minute}

	doer := &countingTLSDoer{}
	var mu sync.Mutex
	builds := 0
	sess := client.NewTLSSession(func(tlsclient.Config) (helper.HTTPDoer, error) {
		mu.Lock()
		builds++
		mu.Unlock()
		return doer, nil
	})
	s.tlsSessions = map[int]*client.TLSSession{RutrackerID: sess}

	ctx := context.Background()
	for i := range 2 {
		tc, err := s.getCached(ctx, RutrackerID)
		if err != nil {
			t.Fatal(err)
		}
		res, err := tc.GetResponse(ctx, "https://rutracker.org/forum/viewtopic.php?t=1", false)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if i == 0 {
			ageCached(s, RutrackerID)
		}
	}
	mu.Lock()
	got := builds
	mu.Unlock()
	if got != 1 || doer.count() != 2 {
		t.Fatalf("builds=%d doer calls=%d", got, doer.count())
	}
	if s.tlsSessions[RutrackerID] != sess {
		t.Fatal("session was replaced")
	}
	if l, d := px.counts(); l != 0 || d != 0 {
		t.Fatalf("flare used: logins=%d destroys=%d", l, d)
	}
}
