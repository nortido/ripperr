package toloka

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"ripperr/internal/model/tracker"
)

func page(s string) *bytes.Buffer { return bytes.NewBufferString(s) }

func TestParseDate(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<tr><td class="row1">Зареєстрований:</td><td class="row2">&nbsp;<span>2026-10-09 13:42</span></td></tr>`
	got := tr.ParseDate(page(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2026, 10, 9, 10, 42, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if _, off := got.Zone(); off != 3*3600 {
		t.Errorf("offset %d, want EEST +03:00", off)
	}
}

func TestParseDateWinterOffset(t *testing.T) {
	tr := New(tracker.Tracker{})
	got := tr.ParseDate(page(`Зареєстрований:</td><td>2026-12-01 10:00</td>`), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	if _, off := got.Zone(); off != 2*3600 {
		t.Errorf("offset %d, want EET +02:00", off)
	}
}

func TestParseDateNoLabel(t *testing.T) {
	tr := New(tracker.Tracker{})
	if got := tr.ParseDate(page(`<span class="postdetails">2026-10-09 13:42</span>`), true); got != nil {
		t.Errorf("post date without the label must not count, got %v", got)
	}
}

func TestParseDateTooFarFromLabel(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `Зареєстрований:` + string(bytes.Repeat([]byte("x"), 400)) + ` 2026-10-09 13:42`
	if got := tr.ParseDate(page(html), true); got != nil {
		t.Errorf("date beyond 300 chars must not count, got %v", got)
	}
}

func TestParseDownloadID(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<a class="piwik_download" href="download.php?id=123456" rel="nofollow">Завантажити</a>`
	if got, want := tr.ParseDownloadID(page(html), true), "123456"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := tr.ParseDownloadID(page(`<a href="viewtopic.php?t=5">x</a>`), true); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestTopicLink(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "toloka.to"})
	if got, want := tr.TopicLink("67558"), "https://toloka.to/t67558"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := tr.TopicLink(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func tolokaData(v string) []*http.Cookie {
	return []*http.Cookie{{Name: "toloka_data", Value: v}}
}

func TestLoginAuthIncomplete(t *testing.T) {
	tr := New(tracker.Tracker{})
	guest := `a%3A2%3A%7Bs%3A11%3A%22autologinid%22%3Bs%3A0%3A%22%22%3Bs%3A6%3A%22userid%22%3Bi%3A-1%3B%7D`
	member := `a%3A2%3A%7Bs%3A11%3A%22autologinid%22%3Bs%3A0%3A%22%22%3Bs%3A6%3A%22userid%22%3Bi%3A4242%3B%7D`
	rawMember := `a:2:{s:11:"autologinid";s:0:"";s:6:"userid";i:4242;}`
	cases := []struct {
		name    string
		cookies []*http.Cookie
		want    bool
	}{
		{"guest userid -1", tolokaData(guest), true},
		{"member userid 4242", tolokaData(member), false},
		{"member unescaped value", tolokaData(rawMember), false},
		{"absent", []*http.Cookie{{Name: "toloka_sid", Value: "abc"}}, true},
		{"no cookies", nil, true},
		{"no userid field", tolokaData(`a%3A0%3A%7B%7D`), true},
	}
	for _, c := range cases {
		if got := tr.LoginAuthIncomplete(c.cookies); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLoginAuthHint(t *testing.T) {
	if New(tracker.Tracker{}).LoginAuthHint() == "" {
		t.Fatal("expected a hint")
	}
}

func TestUTF8PageNotEncoded(t *testing.T) {
	tr := New(tracker.Tracker{})
	if got := tr.ParseDate(page(`Зареєстрований:</td><td>2026-10-09 13:42</td>`), false); got == nil {
		t.Error("UTF-8 page with encoded=false must still parse the date")
	}
}

func TestPickNeedsDownloadID(t *testing.T) {
	tr := New(tracker.Tracker{})
	held := tracker.Held{Stored: true, TopicID: "123456"}
	got := tr.PickTorrent(page(`<a href="download.php?id=123456">Завантажити</a>`), true, held)
	if got.DownloadID != "123456" || got.Miss != "" {
		t.Errorf("got %+v, want id 123456", got)
	}
	got = tr.PickTorrent(page(`<a href="viewtopic.php?t=5">x</a>`), true, held)
	if got.Miss == "" || got.DownloadID != "" {
		t.Errorf("got %+v, want Miss: no download link", got)
	}
}

func TestDetectLegacyHost(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "toloka.to"})
	for link, want := range map[string]bool{
		"http://toloka.hurtom.com/viewtopic.php?t=123":         true,
		"https://toloka.hurtom.com/t123":                       true,
		"https://TOLOKA.hurtom.com:443/t1":                     true,
		"https://www.hurtom.com/portal/":                       false,
		"https://thumb.hurtom.com/image/w250/x.jpg":            false,
		"https://evil.example/?u=https://toloka.hurtom.com/t1": false,
	} {
		if got := tr.Detect(link); got != want {
			t.Errorf("Detect(%q) = %v, want %v", link, got, want)
		}
	}
}

func TestPageLinkLegacyHost(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "toloka.to"})
	for link, want := range map[string]string{
		"http://toloka.hurtom.com/viewtopic.php?t=123": "https://toloka.to/viewtopic.php?t=123",
		"https://toloka.hurtom.com:443/t5":             "https://toloka.to/t5",
	} {
		if got := tr.PageLink(link); got != want {
			t.Errorf("PageLink(%q) = %q, want %q", link, got, want)
		}
	}
}

func TestPageLinkCurrentUnchanged(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "toloka.to"})
	for _, link := range []string{"https://toloka.to/t5", "http://toloka.to/t5"} {
		if got := tr.PageLink(link); got != link {
			t.Errorf("PageLink(%q) = %q, want unchanged", link, got)
		}
	}
}
