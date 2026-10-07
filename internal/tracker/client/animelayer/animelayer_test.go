package animelayer

import (
	"bytes"
	"testing"
	"time"

	"ripperr/internal/model/tracker"
)

func page(s string) *bytes.Buffer { return bytes.NewBufferString(s) }

var msk = time.FixedZone("MSK", 3*60*60)

func pinNow(t *testing.T, at time.Time) {
	t.Helper()
	prev := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = prev })
}

func TestPickFromURL(t *testing.T) {
	tr := New(tracker.Tracker{})
	pick := tr.PickTorrent(page(""), true, tracker.Held{
		URL:     "https://animelayer.ru/torrent/6ac52355ea8427b707036c82/",
		TopicID: "6",
	})
	if pick.DownloadID != "6ac52355ea8427b707036c82" || pick.Miss != "" {
		t.Errorf("got %+v", pick)
	}
	if pick.Date != nil {
		t.Errorf("date must stay with ParseDate, got %v", pick.Date)
	}
}

func TestPickMiss(t *testing.T) {
	tr := New(tracker.Tracker{})
	for _, u := range []string{
		"https://animelayer.ru/torrent/",
		"https://animelayer.ru/torrent/12345/",
		"https://animelayer.ru/torrent/6ac52355ea8427b707036c8/",
		"",
	} {
		pick := tr.PickTorrent(page(""), true, tracker.Held{URL: u, TopicID: "6"})
		if pick.DownloadID != "" || pick.Miss == "" {
			t.Errorf("%q: got %+v, want a Miss", u, pick)
		}
	}
}

func TestDownloadLink(t *testing.T) {
	tr := New(tracker.Tracker{DownloadURL: "https://animelayer.ru/torrent/"})
	if got, want := tr.DownloadLink("6ac52355ea8427b707036c82"), "https://animelayer.ru/torrent/6ac52355ea8427b707036c82/download/"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for _, id := range []string{"6", "", "6AC52355EA8427B707036C82", "6ac52355ea8427b707036c82/../x"} {
		if got := tr.DownloadLink(id); got != "" {
			t.Errorf("%q: got %q, want empty", id, got)
		}
	}
}

func TestTopicLink(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "animelayer.ru"})
	if got, want := tr.TopicLink("6ac52355ea8427b707036c82"), "https://animelayer.ru/torrent/6ac52355ea8427b707036c82/"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := tr.TopicLink(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestParseDateUpdated(t *testing.T) {
	pinNow(t, time.Date(2026, 10, 22, 12, 0, 0, 0, msk))
	tr := New(tracker.Tracker{})
	html := `<span class="date-updated">21 октября в&nbsp;21:32</span>
<span class="date-created">6 октября в&nbsp;19:35</span>`
	got := tr.ParseDate(page(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	if want := time.Date(2026, 10, 21, 21, 32, 0, 0, msk); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateYearRollback(t *testing.T) {
	pinNow(t, time.Date(2027, 1, 2, 12, 0, 0, 0, msk))
	tr := New(tracker.Tracker{})
	got := tr.ParseDate(page(`<span class="date-updated">30 декабря в 10:00</span>`), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	if want := time.Date(2026, 12, 30, 10, 0, 0, 0, msk); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateExplicitYear(t *testing.T) {
	pinNow(t, time.Date(2026, 10, 22, 12, 0, 0, 0, msk))
	tr := New(tracker.Tracker{})
	got := tr.ParseDate(page(`<span class="date-updated">3 октября 2019 в 6:02</span>`), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	if want := time.Date(2019, 10, 3, 6, 2, 0, 0, msk); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateCreatedFallback(t *testing.T) {
	pinNow(t, time.Date(2026, 10, 22, 12, 0, 0, 0, msk))
	tr := New(tracker.Tracker{})
	got := tr.ParseDate(page(`<span class="date-created">6 октября в&nbsp;19:35</span>`), true)
	if got == nil {
		t.Fatal("expected created date as the baseline")
	}
	if want := time.Date(2026, 10, 6, 19, 35, 0, 0, msk); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateNoMatch(t *testing.T) {
	pinNow(t, time.Date(2026, 10, 22, 12, 0, 0, 0, msk))
	tr := New(tracker.Tracker{})
	for name, html := range map[string]string{
		"no span":       `<p>21 октября в 21:32</p>`,
		"unknown month": `<span class="date-updated">21 foo в 21:32</span>`,
	} {
		if got := tr.ParseDate(page(html), true); got != nil {
			t.Errorf("%s: got %v, want nil", name, got)
		}
	}
}

func TestParsePoster(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "animelayer.ru"})
	html := `<img src="https://animelayer.ru/logo.png"><div class="cover"><a href="#"></a><img width="240" height="360" src="https://images.animelayer.ru/SLpkf/240x360/1/crop/c4/05/a.jpg" alt="x"></div>`
	if got, want := tr.ParsePoster(page(html), true), "https://images.animelayer.ru/SLpkf/240x360/1/crop/c4/05/a.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := tr.ParsePoster(page(`<img src="https://animelayer.ru/logo.png">`), true); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestUTF8PageNotEncoded(t *testing.T) {
	pinNow(t, time.Date(2026, 10, 22, 12, 0, 0, 0, msk))
	tr := New(tracker.Tracker{})
	if got := tr.ParseDate(page(`<span class="date-updated">21 октября в 21:32</span>`), false); got == nil {
		t.Error("UTF-8 page with encoded=false must still parse the date")
	}
}
