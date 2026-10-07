package riperam

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"ripperr/internal/model/tracker"
)

func page(s string) *bytes.Buffer { return bytes.NewBufferString(s) }

var msk = time.FixedZone("MSK", 3*60*60)

func TestParseDateBracket(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<tr><td class="gensmall">Торрент</td><td class="gensmall">21 окт 2026, 22:02 [ 21 окт 2026, 22:02 ]</td></tr>`
	got := tr.ParseDate(page(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	if want := time.Date(2026, 10, 21, 22, 2, 0, 0, msk); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateBracketDiffers(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<td>Торрент</td><td class="x">3 сен 2026, 09:15 [ 21 окт 2026, 22:02 ]</td>`
	got := tr.ParseDate(page(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	if want := time.Date(2026, 10, 21, 22, 2, 0, 0, msk); !got.Equal(want) {
		t.Errorf("bracket date must win: got %v, want %v", got, want)
	}
}

func TestParseDateMay(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<td>Торрент</td><td>1 мая 2026, 08:00 [ 3 мая 2026, 10:00 ]</td>`
	got := tr.ParseDate(page(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	if want := time.Date(2026, 5, 3, 10, 0, 0, 0, msk); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateNoMatch(t *testing.T) {
	tr := New(tracker.Tracker{})
	cases := map[string]string{
		"no bracket":    `<td>Торрент</td><td>21 окт 2026, 22:02</td>`,
		"no row":        `<p>21 окт 2026, 22:02 [ 21 окт 2026, 22:02 ]</p>`,
		"unknown month": `<td>Торрент</td><td>1 abc 2026, 08:00 [ 3 abc 2026, 10:00 ]</td>`,
	}
	for name, html := range cases {
		if got := tr.ParseDate(page(html), true); got != nil {
			t.Errorf("%s: got %v, want nil", name, got)
		}
	}
}

func TestParseDownloadID(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<a href="./download/file.php?id=88123&amp;sid=abc">Скачать</a>`
	if got, want := tr.ParseDownloadID(page(html), true), "88123"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := tr.ParseDownloadID(page(`<a href="viewtopic.php?t=1">x</a>`), true); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestParsePoster(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "riperam.org"})
	html := `<a href="https://lostpix.com/img/abc.jpg" rel="prettyPhotoPosters[0]"><img src="https://lostpix.com/img/abc.jpg" alt="" style="max-width: 400px;"/></a>`
	if got, want := tr.ParsePoster(page(html), true), "https://lostpix.com/img/abc.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := tr.ParsePoster(page(`<a href="https://x/y.jpg"><img src="https://x/y.jpg"></a>`), true); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestTopicLink(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "riperam.org"})
	if got, want := tr.TopicLink("441153"), "https://riperam.org/viewtopic.php?t=441153"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := tr.TopicLink(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestLoginAuthIncomplete(t *testing.T) {
	tr := New(tracker.Tracker{})
	cookie := func(name, value string) []*http.Cookie {
		return []*http.Cookie{{Name: name, Value: value}}
	}
	cases := []struct {
		name    string
		cookies []*http.Cookie
		want    bool
	}{
		{"guest user id 1", cookie("phpbb_443623_u", "1"), true},
		{"member user id 55", cookie("phpbb_443623_u", "55"), false},
		{"empty value", cookie("phpbb_443623_u", ""), true},
		{"other cookie only", cookie("phpbb_443623_sid", "abc"), true},
		{"similar name", cookie("phpbb_443623_uid", "55"), true},
		{"no cookies", nil, true},
	}
	for _, c := range cases {
		if got := tr.LoginAuthIncomplete(c.cookies); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLoginAuthHint(t *testing.T) {
	if New(tracker.Tracker{}).LoginAuthHint() == "" {
		t.Fatal("expected a hint about form tokens and cookies")
	}
}

func TestUTF8PageNotEncoded(t *testing.T) {
	tr := New(tracker.Tracker{})
	if got := tr.ParseDate(page(`<td>Торрент</td><td>21 окт 2026, 22:02 [ 21 окт 2026, 22:02 ]</td>`), false); got == nil {
		t.Error("UTF-8 page with encoded=false must still parse the date")
	}
}

func TestPickNeedsDownloadID(t *testing.T) {
	tr := New(tracker.Tracker{})
	got := tr.PickTorrent(page(`<a href="./download/file.php?id=88123&amp;sid=abc">Скачать</a>`), true, tracker.Held{})
	if got.DownloadID != "88123" || got.Miss != "" {
		t.Errorf("got %+v, want id 88123", got)
	}
	got = tr.PickTorrent(page(`<a href="viewtopic.php?t=1">x</a>`), true, tracker.Held{Stored: true, TopicID: "1"})
	if got.Miss == "" || got.DownloadID != "" {
		t.Errorf("got %+v, want Miss: no download link", got)
	}
}
