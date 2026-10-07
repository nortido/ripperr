package tapochek

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"

	"ripperr/internal/model/tracker"
)

var msk = time.FixedZone("MSK", 3*60*60)

const trackerRow = `<tr><td class="row1">Трекер:</td><td class="row2"><span>07-03-2026 03:37</span></td></tr>`

func TestParseDate(t *testing.T) {
	tr := New(tracker.Tracker{})
	got := tr.ParseDate(bytes.NewBufferString(trackerRow), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2026, time.March, 7, 3, 37, 0, 0, msk)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateRegisteredSpanText(t *testing.T) {
	tr := New(tracker.Tracker{})
	page := `<td>Зарегистрирован &nbsp; [ <span title="10 часов">04-09-2026 00:16</span> ]</td>`
	got := tr.ParseDate(bytes.NewBufferString(page), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2026, time.September, 4, 0, 16, 0, 0, msk)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateUnknownFormatNil(t *testing.T) {
	tr := New(tracker.Tracker{})
	page := `<td>Зарегистрирован &nbsp; [ <span title="10 часов">вчера</span> ]</td>`
	if got := tr.ParseDate(bytes.NewBufferString(page), true); got != nil {
		t.Errorf("unknown format accepted: %v", got)
	}
}

func TestParseDateGuestPostOnly(t *testing.T) {
	tr := New(tracker.Tracker{})
	page := `<a class="small" href="./viewtopic.php?p=3106548#3106548" title="Линк на это сообщение">07-Мар-2026 03:37</a>`
	if got := tr.ParseDate(bytes.NewBufferString(page), true); got != nil {
		t.Errorf("post date used as torrent date: %v", got)
	}
}

func TestParseDateCP1251(t *testing.T) {
	raw, err := charmap.Windows1251.NewEncoder().Bytes([]byte(trackerRow))
	if err != nil {
		t.Fatal(err)
	}
	tr := New(tracker.Tracker{})
	got := tr.ParseDate(bytes.NewBuffer(raw), false)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2026, time.March, 7, 3, 37, 0, 0, msk)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDownloadID(t *testing.T) {
	tr := New(tracker.Tracker{})
	page := `<a href="profile.php?mode=register">x</a><a href="download.php?id=123456" class="genmed">Скачать</a><a href="download.php?id=999">`
	if got := tr.ParseDownloadID(bytes.NewBufferString(page), true); got != "123456" {
		t.Errorf("got %q, want 123456", got)
	}
	guest := `<h1 class="attach_link"><a href="profile.php?mode=register">Скачать</a></h1>`
	if got := tr.ParseDownloadID(bytes.NewBufferString(guest), true); got != "" {
		t.Errorf("guest page gave %q", got)
	}
}

func TestParsePoster(t *testing.T) {
	tr := New(tracker.Tracker{})
	page := `<div style="text-align: center;"><var class="postImg" title="https://i4.imageban.ru/out/2026/03/07/d71ae44c898f46f3f70789b88979f2dc.png"></var>`
	want := "https://i4.imageban.ru/out/2026/03/07/d71ae44c898f46f3f70789b88979f2dc.png"
	if got := tr.ParsePoster(bytes.NewBufferString(page), true); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTopicLink(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "tapochek.net"})
	if got := tr.TopicLink("284595"); got != "https://tapochek.net/viewtopic.php?t=284595" {
		t.Errorf("got %q", got)
	}
	if got := tr.TopicLink(""); got != "" {
		t.Errorf("empty id gave %q", got)
	}
}

func TestLoginAuthIncomplete(t *testing.T) {
	tr := New(tracker.Tracker{})
	if !tr.LoginAuthIncomplete(nil) {
		t.Fatal("expected incomplete for nil cookies")
	}
	if !tr.LoginAuthIncomplete([]*http.Cookie{{Name: "bb_data", Value: ""}}) {
		t.Fatal("expected incomplete for empty bb_data")
	}
	if tr.LoginAuthIncomplete([]*http.Cookie{{Name: "bb_data", Value: "a%3A3"}}) {
		t.Fatal("expected complete with bb_data")
	}
	if tr.LoginAuthHint() == "" {
		t.Fatal("expected credentials format hint")
	}
}

func TestPickNeedsDownloadID(t *testing.T) {
	tr := New(tracker.Tracker{})
	got := tr.PickTorrent(bytes.NewBufferString(`<a href="download.php?id=123456" class="genmed">Скачать</a>`), true, tracker.Held{})
	if got.DownloadID != "123456" || got.Miss != "" {
		t.Errorf("got %+v, want id 123456", got)
	}
	guest := `<h1 class="attach_link"><a href="profile.php?mode=register">Скачать</a></h1>`
	got = tr.PickTorrent(bytes.NewBufferString(guest), true, tracker.Held{Stored: true, TopicID: "77"})
	if got.Miss == "" || got.DownloadID != "" {
		t.Errorf("got %+v, want Miss: no download link", got)
	}
}
