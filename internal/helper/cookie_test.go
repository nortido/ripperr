package helper

import (
	"net/http"
	"testing"
)

func TestParseCookies(t *testing.T) {
	cookies := ParseCookies("a=1; b=2; ; c=3")
	if len(cookies) != 3 {
		t.Fatalf("expected 3 cookies, got %d: %+v", len(cookies), cookies)
	}
	want := map[string]string{"a": "1", "b": "2", "c": "3"}
	for _, c := range cookies {
		if want[c.Name] != c.Value {
			t.Errorf("cookie %s: got %s, want %s", c.Name, c.Value, want[c.Name])
		}
	}
	if got := ParseCookies(""); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestMergeCookies(t *testing.T) {
	base := []*http.Cookie{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}}
	extra := []*http.Cookie{{Name: "b", Value: "20"}, {Name: "c", Value: "3"}}
	merged := MergeCookies(base, extra)
	if len(merged) != 3 {
		t.Fatalf("expected 3 cookies, got %d", len(merged))
	}
	got := map[string]string{}
	for _, c := range merged {
		got[c.Name] = c.Value
	}
	if got["b"] != "20" {
		t.Errorf("expected extra to override base for 'b', got %s", got["b"])
	}
	if got["a"] != "1" || got["c"] != "3" {
		t.Errorf("expected base/extra-only cookies to be kept, got %v", got)
	}
}

func TestCookiesString(t *testing.T) {
	cookies := []*http.Cookie{{Name: "a", Value: "1"}, {Name: "b", Value: "2"}}
	if got, want := CookiesString(cookies), "a=1; b=2"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
