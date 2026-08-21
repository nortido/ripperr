package tracker

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	trackerModel "trackerr/internal/model/tracker"
	"trackerr/internal/tracker/client"
	"trackerr/internal/tracker/client/kinozal"
)

type stubTracker struct {
	trackerModel.Tracker
}

func TestSelectActiveDomainNoMirrors(t *testing.T) {
	called := false
	alive := func(context.Context, string) bool {
		called = true
		return true
	}
	got := selectActiveDomain(context.Background(), "primary.org", nil, alive)
	if got != "primary.org" || called {
		t.Fatalf("got %q called=%v", got, called)
	}
}

func TestSelectActiveDomainPrimaryAlive(t *testing.T) {
	alive := func(_ context.Context, domain string) bool {
		return domain == "primary.org"
	}
	got := selectActiveDomain(context.Background(), "primary.org", []string{"mirror.org"}, alive)
	if got != "primary.org" {
		t.Fatalf("got %q", got)
	}
}

func TestTrackerActiveDomainOverridesSelection(t *testing.T) {
	tr := &stubTracker{Tracker: trackerModel.Tracker{
		Domain:       "primary.org",
		ActiveDomain: "picked.example",
		Mirrors:      []string{"mirror.org"},
	}}
	active := trackerActiveDomain(tr)
	if active != "picked.example" {
		t.Fatalf("trackerActiveDomain = %q", active)
	}
}

func TestSelectActiveDomainMirrorFallback(t *testing.T) {
	alive := func(_ context.Context, domain string) bool {
		return domain == "mirror.org"
	}
	got := selectActiveDomain(context.Background(), "primary.org", []string{"mirror.org"}, alive)
	if got != "mirror.org" {
		t.Fatalf("got %q", got)
	}
}

func TestSelectActiveDomainAllDead(t *testing.T) {
	alive := func(context.Context, string) bool { return false }
	got := selectActiveDomain(context.Background(), "primary.org", []string{"mirror.org"}, alive)
	if got != "primary.org" {
		t.Fatalf("got %q", got)
	}
}

func TestWithActiveDomainReplacesHosts(t *testing.T) {
	tr := &stubTracker{Tracker: trackerModel.Tracker{
		Name:        "rutracker",
		Domain:      "rutracker.org",
		LoginURL:    "https://rutracker.org/forum/login.php",
		DownloadURL: "https://rutracker.org/forum/dl.php?t=",
	}}
	wrapped := withActiveDomain(tr, "mirror.example")
	if wrapped.GetDomain() != "mirror.example" {
		t.Fatalf("domain = %q", wrapped.GetDomain())
	}
	if wrapped.LoginLink() != "https://mirror.example/forum/login.php" {
		t.Fatalf("login = %q", wrapped.LoginLink())
	}
	if wrapped.DownloadLink("42") != "https://mirror.example/forum/dl.php?t=42" {
		t.Fatalf("download = %q", wrapped.DownloadLink("42"))
	}
	if !wrapped.Detect("https://rutracker.org/forum/viewtopic.php?t=1") {
		t.Fatal("detect should still match primary domain")
	}
}

func TestDomainOverrideForwardsLoginAuth(t *testing.T) {
	tr := kinozal.New(trackerModel.Tracker{Name: "kinozal", Domain: "kinozal.tv"})
	wrapped := withActiveDomain(tr, "kinozal.guru")
	v, ok := wrapped.(interface {
		LoginAuthIncomplete([]*http.Cookie) bool
		LoginAuthHint() string
	})
	if !ok {
		t.Fatal("domainOverride should forward login auth validator")
	}
	if !v.LoginAuthIncomplete(nil) {
		t.Fatal("expected incomplete auth through wrapper")
	}
	if v.LoginAuthHint() == "" {
		t.Fatal("expected hint through wrapper")
	}
}

var _ client.ITracker = (*stubTracker)(nil)

type posterTracker struct {
	client.ITracker
	domain string
	poster string
}

func (p *posterTracker) GetDomain() string                      { return p.domain }
func (p *posterTracker) ParsePoster(*bytes.Buffer, bool) string { return p.poster }

func TestDomainOverrideParsePosterFollowsMirror(t *testing.T) {
	base := &posterTracker{domain: "kinozal.tv", poster: "https://kinozal.tv/i/poster/1.jpg"}
	d := &domainOverride{ITracker: base, domain: "kinozal.guru"}
	if got, want := d.ParsePoster(nil, false), "https://kinozal.guru/i/poster/1.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDomainOverrideParsePosterKeepsForeignHost(t *testing.T) {
	base := &posterTracker{domain: "kinozal.tv", poster: "https://s.kinozal.tv/i/poster/1.jpg"}
	d := &domainOverride{ITracker: base, domain: "kinozal.guru"}
	if got, want := d.ParsePoster(nil, false), "https://s.kinozal.tv/i/poster/1.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
