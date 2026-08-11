package kinozal

import (
	"bytes"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"

	"trackerr/internal/model/tracker"
)

const detailsMarkup = `<li class="tp">Характеристика</li>
<li>Вес<span class="floatright green n">33.6 ГБ (36,081,699,046)</span></li>
<li>Залит<span class="floatright green n">31 мая 2021 в 20:43</span></li>
<li>Обновлен<span class="floatright green n">1 июня 2023 в 18:38</span></li>`

func TestParseDate_Today(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<div class="bx1 justify"><b>Торрент-файл обновлен сегодня в 10:30</b>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	now := time.Now()
	if got.Year() != now.Year() || got.Month() != now.Month() || got.Day() != now.Day() {
		t.Errorf("expected today's date, got %v (now %v)", got, now)
	}
	if got.Hour() != 10 || got.Minute() != 30 {
		t.Errorf("expected 10:30, got %02d:%02d", got.Hour(), got.Minute())
	}
}

func TestParseDate_YesterdayFallback(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<li>Залит<span class="floatright green n">вчера в 08:15</span>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Now().AddDate(0, 0, -1)
	if got.Year() != want.Year() || got.Month() != want.Month() || got.Day() != want.Day() {
		t.Errorf("expected yesterday's date, got %v, want day %v", got, want)
	}
	if got.Hour() != 8 || got.Minute() != 15 {
		t.Errorf("expected 08:15, got %02d:%02d", got.Hour(), got.Minute())
	}
}

func TestParseDate_ExplicitDate(t *testing.T) {
	tr := New(tracker.Tracker{})
	html := `<div class="bx1 justify"><b>Торрент-файл обновлен 5 03 2024 в 09:00</b>`
	got := tr.ParseDate(bytes.NewBufferString(html), true)
	if got == nil {
		t.Fatal("expected parsed date")
	}
	want := time.Date(2024, time.March, 5, 9, 0, 0, 0, time.UTC)
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
	want := time.Date(2023, time.June, 1, 18, 38, 0, 0, time.UTC)
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
	want := time.Date(2021, time.May, 31, 20, 43, 0, 0, time.UTC)
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
	want := time.Date(2023, time.June, 1, 18, 38, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
