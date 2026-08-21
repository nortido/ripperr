package rutor

import (
	"bytes"
	"testing"
	"time"

	"trackerr/internal/model/tracker"
)

const page = `<div id="download">
<a href="magnet:?xt=urn:btih:517820b9&amp;dn=rutor.info_Test&amp;tr=udp://opentor.net:6969"><img src="//cdnbunny.org/i/magnet.gif"></a>
<a href="//d.rutor.info/download/1000000"><img src="//cdnbunny.org/t/down.png"> Скачать Test.torrent</a>
</div>
<tr><td class="header">Сидер замечен</td><td>19-08-2026 22:19:11 (19 часов назад)</td></tr>
<tr><td class="header">Добавлен</td><td>31-08-2024 16:43:45  (24 месяца назад)</td></tr>
<div id="index"><a href="magnet:?xt=urn:btih:deadbeef&dn=related">related</a></div>`

func TestParseDate(t *testing.T) {
	tr := New(tracker.Tracker{})
	got := tr.ParseDate(bytes.NewBufferString(page), false)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2024, time.August, 31, 16, 43, 45, 0, time.FixedZone("MSK", 3*60*60))
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateSingleDigitHour(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<tr><td class="header">Добавлен</td><td>29-06-2023 6:14:07  (давно)</td></tr>`
	got := tr.ParseDate(bytes.NewBufferString(html), false)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	if got.Hour() != 6 {
		t.Errorf("got hour %d, want 6", got.Hour())
	}
}

func TestParseDatePinnedTopic(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<tr><td class="header">Добавлен</td><td>19-01-2038 6:14:07  (через 11 лет)</td></tr>`
	if got := tr.ParseDate(bytes.NewBufferString(html), false); got != nil {
		t.Errorf("future date accepted: %v", got)
	}
}

func TestParseDateJustAdded(t *testing.T) {
	tr := New(tracker.Tracker{})
	for name, tm := range map[string]time.Time{
		"now":              time.Now(),
		"clock skew ahead": time.Now().Add(12 * time.Hour),
	} {
		html := `<tr><td class="header">Добавлен</td><td>` + tm.In(mskZone).Format("02-01-2006 15:04:05") + `  (только что)</td></tr>`
		if got := tr.ParseDate(bytes.NewBufferString(html), false); got == nil {
			t.Errorf("%s: date rejected", name)
		}
	}
}

func TestParseDateNoMatch(t *testing.T) {
	tr := New(tracker.Tracker{})
	if got := tr.ParseDate(bytes.NewBufferString(`<td class="header">Добавлен</td><td>скоро</td>`), false); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestParseMagnet(t *testing.T) {
	tr := New(tracker.Tracker{})
	got := tr.ParseMagnet(bytes.NewBufferString(page), false)
	want := "magnet:?xt=urn:btih:517820b9&dn=rutor.info_Test&tr=udp://opentor.net:6969"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseMagnetNoMatch(t *testing.T) {
	tr := New(tracker.Tracker{})
	if got := tr.ParseMagnet(bytes.NewBufferString(`<div id="index"><a href="magnet:?xt=urn:btih:deadbeef">x</a></div>`), false); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestTopicLink(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rutor.info"})
	if got := tr.TopicLink("1000000"); got != "https://rutor.info/torrent/1000000" {
		t.Errorf("got %q", got)
	}
	if got := tr.TopicLink(""); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestParseMagnetDownloadBlockWithoutMagnet(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<div id="download">
<a href="//d.rutor.info/download/922808"><img src="//cdnbunny.org/t/down.png"> Скачать</a>
</div>
<div id="index"><a href="magnet:?xt=urn:btih:deadbeef&amp;dn=related">related</a></div>`
	if got := tr.ParseMagnet(bytes.NewBufferString(html), false); got != "" {
		t.Errorf("magnet of a related torrent leaked: %q", got)
	}
}

func TestParseMagnetIgnoresMagnetAboveDownloadBlock(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<a href="magnet:?xt=urn:btih:aaaa&amp;dn=header">header</a>` + page
	got := tr.ParseMagnet(bytes.NewBufferString(html), false)
	want := "magnet:?xt=urn:btih:517820b9&dn=rutor.info_Test&tr=udp://opentor.net:6969"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParsePoster(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rutor.info"})
	html := `<img src="//cdnbunny.org/logo.jpg" alt="rutor.info logo" />
<img src="//cdnbunny.org/i/magnet.gif">
<table id="details"><tr><td></td><td><br /><img src="https://i123.fastpic.org/big/2024/0830/cb/poster.jpg" /><br /><b>Название: </b>Test`
	if got, want := tr.ParsePoster(bytes.NewBufferString(html), false), "https://i123.fastpic.org/big/2024/0830/cb/poster.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParsePosterProtocolRelative(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rutor.info"})
	html := `<table id="details"><tr><td><img src="//i123.fastpic.org/big/poster.jpg" /></td></tr></table>`
	if got, want := tr.ParsePoster(bytes.NewBufferString(html), false), "https://i123.fastpic.org/big/poster.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParsePosterNoMatch(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rutor.info"})
	if got := tr.ParsePoster(bytes.NewBufferString(`<img src="//cdnbunny.org/logo.jpg">`), false); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestParsePosterPlainImageTag(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "rutor.info"})
	html := `<table id="details"><tr><td style="vertical-align:top;"></td><td><br /><img src="https://i3.imageban.ru/out/2023/04/26/a9574d66f990c18d110ecec8d0635c91.png"><br /><b>Название: </b>Test`
	if got, want := tr.ParsePoster(bytes.NewBufferString(html), false), "https://i3.imageban.ru/out/2023/04/26/a9574d66f990c18d110ecec8d0635c91.png"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
