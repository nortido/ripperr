package tracker

import (
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
