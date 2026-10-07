package rustorka

import (
	"bytes"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"

	"ripperr/internal/model/tracker"
)

const registeredRow = "<tr><td>Зарегистрирован:</td>\r\n\r\n    <td>22-Окт-26 05:59</td></tr>"

func TestParseDate(t *testing.T) {
	tr := New(tracker.Tracker{})
	got := tr.ParseDate(bytes.NewBufferString(registeredRow), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2026, time.October, 22, 5, 59, 0, 0, time.FixedZone("MSK", 3*60*60))
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateCP1251(t *testing.T) {
	raw, err := charmap.Windows1251.NewEncoder().Bytes([]byte(registeredRow))
	if err != nil {
		t.Fatal(err)
	}
	tr := New(tracker.Tracker{})
	got := tr.ParseDate(bytes.NewBuffer(raw), false)
	if got == nil || got.Month() != time.October || got.Day() != 22 {
		t.Errorf("got %v", got)
	}
}

func TestParseDateUnknownMonthNil(t *testing.T) {
	tr := New(tracker.Tracker{})
	page := `<td>Зарегистрирован:</td><td>22-Абв-26 05:59</td>`
	if got := tr.ParseDate(bytes.NewBufferString(page), true); got != nil {
		t.Errorf("unknown month accepted: %v", got)
	}
}

func TestParseDownloadID(t *testing.T) {
	tr := New(tracker.Tracker{})
	page := `<a href="download.php?id=4581234" class="genmed">Скачать</a>`
	if got := tr.ParseDownloadID(bytes.NewBufferString(page), true); got != "4581234" {
		t.Errorf("got %q, want 4581234", got)
	}
}

func TestTopicLink(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rustorka.com"})
	if got := tr.TopicLink("300524"); got != "https://rustorka.com/forum/viewtopic.php?t=300524" {
		t.Errorf("got %q", got)
	}
}

func TestPickNeedsDownloadID(t *testing.T) {
	tr := New(tracker.Tracker{})
	got := tr.PickTorrent(bytes.NewBufferString(`<a href="download.php?id=4581234" class="genmed">Скачать</a>`), true, tracker.Held{})
	if got.DownloadID != "4581234" || got.Miss != "" {
		t.Errorf("got %+v, want id 4581234", got)
	}
	got = tr.PickTorrent(bytes.NewBufferString(registeredRow), true, tracker.Held{Stored: true, TopicID: "77"})
	if got.Miss == "" || got.DownloadID != "" {
		t.Errorf("got %+v, want Miss: no download link", got)
	}
}
