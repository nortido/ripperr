package rutracker

import (
	"bytes"
	"testing"
	"time"

	"ripperr/internal/model/tracker"
)

func TestParseDate_PostedSince(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<span class="posted_since hide-for-print">(добавлена, ред. 15-03-24 10:30)</span>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2024, time.March, 15, 10, 30, 0, 0, time.FixedZone("MSK", 3*60*60))
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
	want := time.Date(2024, time.March, 15, 10, 30, 0, 0, time.FixedZone("MSK", 3*60*60))
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

func TestParsePoster(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rutracker.org"})
	html := `<div class="post_body"><var class="postImg postImgAligned img-right" title="https://static.rutracker.cc/tor/poster.jpg">&nbsp;</var>Описание`
	if got, want := tr.ParsePoster(bytes.NewBufferString(html), true), "https://static.rutracker.cc/tor/poster.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParsePosterNoMatch(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rutracker.org"})
	if got := tr.ParsePoster(bytes.NewBufferString(`<div class="post_body">Описание</div>`), true); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestParsePosterPrefersAlignedImage(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rutracker.org"})
	// порядок как на странице релиза: прижатый вправо постер и отдельно скриншоты
	html := `<img class="postImg postImgAligned img-right" alt="pic" src="https://i128.fastpic.org/big/2026/0807/bb/poster.png?r=1">
<var class="postImg" title="https://i128.fastpic.org/big/2026/0807/5c/screen1.png"></var>
<var class="postImg" title="https://i128.fastpic.org/big/2026/0807/06/screen2.png"></var>`
	if got, want := tr.ParsePoster(bytes.NewBufferString(html), true), "https://i128.fastpic.org/big/2026/0807/bb/poster.png?r=1"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParsePosterAlignedWinsRegardlessOfOrder(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rutracker.org"})
	html := `<var class="postImg" title="https://i128.fastpic.org/big/2026/0807/5c/screen1.png"></var>
<var class="postImg postImgAligned img-right" title="https://i128.fastpic.org/big/2026/0807/bb/poster.png"></var>`
	if got, want := tr.ParsePoster(bytes.NewBufferString(html), true), "https://i128.fastpic.org/big/2026/0807/bb/poster.png"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParsePosterFallsBackToFirstPostImg(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rutracker.org"})
	html := `<var class="postImg" title="https://i128.fastpic.org/big/2026/0807/5c/screen1.png"></var>`
	if got, want := tr.ParsePoster(bytes.NewBufferString(html), true), "https://i128.fastpic.org/big/2026/0807/5c/screen1.png"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParsePosterBrowserRenderedTakesTitleOverPlaceholder(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rutracker.org"})
	// после FlareSolverr картинка не загрузилась: в src заглушка, адрес остался в title
	html := `<img src="https://static.rutracker.cc/templates/v1/images/broken_image_1.svg" class="postImg post-img-broken" title="https://i128.fastpic.org/big/2026/0919/82/screen.jpg" alt="x">
<img src="https://static.rutracker.cc/templates/v1/images/broken_image_1.svg" class="postImg postImgAligned img-right post-img-broken" title="https://i128.fastpic.org/big/2026/0919/70/poster.jpg" alt="x">`
	if got, want := tr.ParsePoster(bytes.NewBufferString(html), true), "https://i128.fastpic.org/big/2026/0919/70/poster.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
