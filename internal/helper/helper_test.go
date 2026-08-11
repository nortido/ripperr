package helper

import (
	"testing"
	"time"
)

func TestSubstr(t *testing.T) {
	cases := []struct {
		name   string
		s      string
		pos    int
		length int
		want   string
	}{
		{"basic ascii", "hello world", 0, 5, "hello"},
		{"middle", "hello world", 6, 5, "world"},
		{"length exceeds string", "hello", 2, 100, "llo"},
		{"unicode runes", "привет мир", 0, 6, "привет"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Substr(c.s, c.pos, c.length); got != c.want {
				t.Errorf("Substr(%q, %d, %d) = %q, want %q", c.s, c.pos, c.length, got, c.want)
			}
		})
	}
}

func TestTimeFromRusDate(t *testing.T) {
	got := TimeFromRusDate("15 марта 2024 10:30", "02 01 2006 15:04")
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	want := time.Date(2024, time.March, 15, 10, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTimeFromRusDate_ShortMonth(t *testing.T) {
	got := TimeFromRusDate("05 Мар 2024 12:00:00", "02 01 2006 15:04:05")
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	want := time.Date(2024, time.March, 5, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTimeFromRusDate_Invalid(t *testing.T) {
	if got := TimeFromRusDate("not a date", "02 01 2006 15:04"); got != nil {
		t.Errorf("expected nil for invalid date, got %v", got)
	}
}
