package kinozal

import (
	"bytes"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"

	"ripperr/internal/model/tracker"
)

const detailsMarkup = `<li class="tp">Характеристика</li>
<li>Вес<span class="floatright green n">33.6 ГБ (36,081,699,046)</span></li>
<li>Залит<span class="floatright green n">31 мая 2021 в 20:43</span></li>
<li>Обновлен<span class="floatright green n">1 июня 2023 в 18:38</span></li>`

func fixNow(t *testing.T, at time.Time) {
	t.Helper()
	old := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = old })
}

func TestParseDate_Today(t *testing.T) {
	fixNow(t, time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC))
	tr := New(tracker.Tracker{})
	html := `<div class="bx1 justify"><b>Торрент-файл обновлен сегодня в 10:30</b>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2026, time.March, 14, 10, 30, 0, 0, time.FixedZone("MSK", 3*60*60))
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDate_YesterdayFallback(t *testing.T) {
	fixNow(t, time.Date(2026, time.March, 14, 12, 0, 0, 0, time.UTC))
	tr := New(tracker.Tracker{})
	html := `<li>Залит<span class="floatright green n">вчера в 08:15</span>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2026, time.March, 13, 8, 15, 0, 0, time.FixedZone("MSK", 3*60*60))
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDate_TodayAfterMidnightMSK(t *testing.T) {
	fixNow(t, time.Date(2026, time.March, 14, 22, 30, 0, 0, time.UTC))
	tr := New(tracker.Tracker{})
	html := `<li>Залит<span class="floatright green n">сегодня в 00:45</span>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2026, time.March, 15, 0, 45, 0, 0, time.FixedZone("MSK", 3*60*60))
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDate_YesterdayAfterMidnightMSK(t *testing.T) {
	fixNow(t, time.Date(2026, time.March, 14, 22, 30, 0, 0, time.UTC))
	tr := New(tracker.Tracker{})
	html := `<li>Залит<span class="floatright green n">вчера в 23:10</span>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2026, time.March, 14, 23, 10, 0, 0, time.FixedZone("MSK", 3*60*60))
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDate_ExplicitDate(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<div class="bx1 justify"><b>Торрент-файл обновлен 5 03 2024 в 09:00</b>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2024, time.March, 5, 9, 0, 0, 0, time.FixedZone("MSK", 3*60*60))
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDate_DetailsObnovlen(t *testing.T) {
	tr := New(tracker.Tracker{})
	got := tr.ParseDate(bytes.NewBufferString(detailsMarkup), true)
	if got == nil {
		t.Fatal("expected parsed date from Обновлен row")
	}
	want := time.Date(2023, time.June, 1, 18, 38, 0, 0, time.FixedZone("MSK", 3*60*60))
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDate_DetailsZalitOnly(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<li>Залит<span class="floatright green n">31 мая 2021 в 20:43</span></li>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date from Залит row")
	}
	want := time.Date(2021, time.May, 31, 20, 43, 0, 0, time.FixedZone("MSK", 3*60*60))
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDate_CP1251Encoded(t *testing.T) {
	tr := New(tracker.Tracker{})
	encoded, err := charmap.Windows1251.NewEncoder().String(detailsMarkup)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.ParseDate(bytes.NewBufferString(encoded), false)
	if got == nil {
		t.Fatal("expected parsed date from CP1251 body")
	}
	want := time.Date(2023, time.June, 1, 18, 38, 0, 0, time.FixedZone("MSK", 3*60*60))
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParsePosterSkipsSiteChrome(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "kinozal.tv"})
	html := `<img src="/pic/logo_kinozal.png?v=3" alt="Кинозал">
<div class="bx1 justify"><img src="//s.kinozal.tv/pic/poster/1234.jpg" class="p200"></div>`
	if got, want := tr.ParsePoster(bytes.NewBufferString(html), true), "https://s.kinozal.tv/pic/poster/1234.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParsePosterRelativePathGetsHost(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "kinozal.tv"})
	html := `<img src="/pic/logo_kinozal.png"><img src="/posters/1234.jpg">`
	if got, want := tr.ParsePoster(bytes.NewBufferString(html), true), "https://kinozal.tv/posters/1234.jpg"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
