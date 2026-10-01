package nnm

import (
	"bytes"
	"testing"
	"time"

	"ripperr/internal/model/tracker"
)

func TestParseDate(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := "Зарегистрирован:&nbsp;</td>\n    <td class=\"genmed\">&nbsp;05 Мар 2024 12:00:00</td>"
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2024, time.March, 5, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDate_NoMatch(t *testing.T) {
	tr := New(tracker.Tracker{})
	if got := tr.ParseDate(bytes.NewBufferString("<html>nothing</html>"), true); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestParseDownloadID(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<a href="download.php?id=12345" rel="nofollow">Скачать</a>`
	if got, want := tr.ParseDownloadID(bytes.NewBufferString(html), true), "12345"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseDownloadID_NoMatch(t *testing.T) {
	tr := New(tracker.Tracker{})
	if got := tr.ParseDownloadID(bytes.NewBufferString("<html></html>"), true); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}
