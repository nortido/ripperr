package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestQueryInt(t *testing.T) {
	cases := []struct {
		name          string
		query         string
		key           string
		def, min, max int
		want          int
	}{
		{"missing uses default", "", "limit", 50, 1, 1000, 50},
		{"zero uses default", "limit=0", "limit", 50, 1, 1000, 50},
		{"negative uses default", "limit=-5", "limit", 50, 1, 1000, 50},
		{"non-numeric uses default", "limit=abc", "limit", 50, 1, 1000, 50},
		{"in range passes through", "limit=200", "limit", 50, 1, 1000, 200},
		{"above max clamps to max", "limit=100000", "limit", 50, 1, 1000, 1000},
		{"offset negative clamps to 0", "offset=-1", "offset", 0, 0, 0, 0},
		{"offset unbounded above", "offset=999999", "offset", 0, 0, 0, 999999},
		{"tracker below 1 becomes 0", "tracker=0", "tracker", 0, 1, 0, 0},
		{"tracker negative becomes 0", "tracker=-1", "tracker", 0, 1, 0, 0},
		{"tracker valid passes through", "tracker=5", "tracker", 0, 1, 0, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/?"+c.query, nil)
			got := queryInt(req, c.key, c.def, c.min, c.max)
			if got != c.want {
				t.Fatalf("queryInt(%q) = %d, want %d", c.query, got, c.want)
			}
		})
	}
}

func TestQueryTrimmed(t *testing.T) {
	req := httptest.NewRequest("GET", "/?name=%20%20bad%20%20", nil)
	if got := queryTrimmed(req, "name", 200); got != "bad" {
		t.Fatalf("queryTrimmed trims whitespace: got %q", got)
	}

	long := strings.Repeat("x", 250)
	req = httptest.NewRequest("GET", "/?name="+long, nil)
	got := queryTrimmed(req, "name", 200)
	if len(got) != 200 {
		t.Fatalf("queryTrimmed ascii len = %d, want 200", len(got))
	}

	// кириллическая строка сильно за пределом в 200 байт: каждая руна по 2 байта, поэтому
	// наивный срез байтов [:200] может попасть в середину руны. хелпер обязан отступить
	// до полной границы руны и остаться валидным UTF-8.
	cyrillic := strings.Repeat("б", 150) // 300 bytes, 150 runes
	req = httptest.NewRequest("GET", "/?name="+cyrillic, nil)
	got = queryTrimmed(req, "name", 200)
	if len(got) > 200 {
		t.Fatalf("queryTrimmed cyrillic len = %d, want <= 200", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("queryTrimmed produced invalid UTF-8: %q", got)
	}
	if len(got) != 200 {
		t.Fatalf("queryTrimmed cyrillic len = %d, want 200 (even byte cap lands on a rune boundary)", len(got))
	}
}
