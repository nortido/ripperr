package teamhd

import (
	"bytes"
	"testing"
	"time"

	"ripperr/internal/model/tracker"
)

func TestParseDate(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<b>Добавлен</b> - 15-03-2024 10:30:00<br />`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2024, time.March, 15, 10, 30, 0, 0, time.FixedZone("MSK", 3*60*60))
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDate_NoMatch(t *testing.T) {
	tr := New(tracker.Tracker{})
	if got := tr.ParseDate(bytes.NewBufferString("<html></html>"), true); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}
