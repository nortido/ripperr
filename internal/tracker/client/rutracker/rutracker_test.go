package rutracker

import (
	"bytes"
	"testing"
	"time"

	"trackerr/internal/model/tracker"
)

func TestParseDate_PostedSince(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<span class="posted_since hide-for-print">(добавлена, ред. 15-03-24 10:30)</span>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want, _ := time.Parse("02-01-06 15:04", "15-03-24 10:30")
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDate_PLinkFallback(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<a class="p-link small" href="viewtopic.php?t=123">15-03-24 10:30</a>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date from fallback regex")
	}
	want, _ := time.Parse("02-01-06 15:04", "15-03-24 10:30")
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDate_NoMatch(t *testing.T) {
	tr := New(tracker.Tracker{})
	if got := tr.ParseDate(bytes.NewBufferString("<html>nothing here</html>"), true); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestParseMagnet(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<a href="magnet:?xt=urn:btih:ABC123&amp;dn=test">magnet</a>`
	got := tr.ParseMagnet(bytes.NewBufferString(html), true)
	want := "magnet:?xt=urn:btih:ABC123&dn=test"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
