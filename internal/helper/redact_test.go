package helper

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestRedactURL(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"userinfo", "http://u:SEC-pw@host:8080", "http://host:8080"},
		{"userinfo only name", "http://SEC-user@host", "http://host"},
		{"path", "https://host/SEC-path", "https://host/***"},
		{"query", "https://host:9/?t=SEC-q", "https://host:9/***"},
		{"query without path", "https://host?t=SEC-q", "https://host/***"},
		{"fragment", "https://host#SEC-f", "https://host/***"},
		{"everything", "http://u:SEC-pw@host:81/SEC-p?t=SEC-q#SEC-f", "http://host:81/***"},
		{"ipv6", "http://[::1]:8080/SEC-p", "http://[::1]:8080/***"},
		{"no path", "socks5://host:1080", "socks5://host:1080"},
		{"slash path", "http://host/", "http://host"},
		{"unparseable", "http://u:SEC-pw@host:bad", "***"},
		{"no scheme", "SEC-host:8080", "***"},
		{"hostless", "socks5://u:SEC-pw@/SEC-path", "***"},
		{"empty", "", "***"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RedactURL(c.raw)
			if got != c.want {
				t.Fatalf("RedactURL(%q) = %q, want %q", c.raw, got, c.want)
			}
			if strings.Contains(got, "SEC-") {
				t.Fatalf("secret left in %q", got)
			}
		})
	}
}

func TestRedactURLError(t *testing.T) {
	if RedactURLError(nil) != nil {
		t.Fatal("nil must stay nil")
	}

	plain := errors.New("plain SEC-text")
	if got := RedactURLError(plain); got != plain {
		t.Fatalf("non-url error changed: %v", got)
	}

	raw := "http://u:SEC-pw@host:8080/SEC-path?t=SEC-q"
	direct := &url.Error{Op: "Post", URL: raw, Err: errors.New("connection refused")}
	got := RedactURLError(direct)
	if strings.Contains(got.Error(), "SEC-") || !strings.Contains(got.Error(), "host:8080") {
		t.Fatalf("direct: %q", got)
	}
	var ue *url.Error
	if !errors.As(got, &ue) {
		t.Fatal("direct: *url.Error lost")
	}

	// fmt.Errorf запоминает текст обертки при создании, поправить одну цепочку мало
	wrapped := fmt.Errorf("webhook: %w", &url.Error{Op: "Post", URL: raw, Err: errors.New("connection refused")})
	got = RedactURLError(wrapped)
	if strings.Contains(got.Error(), "SEC-") || !strings.Contains(got.Error(), "host:8080") || !strings.HasPrefix(got.Error(), "webhook: ") {
		t.Fatalf("wrapped: %q", got)
	}
	ue = nil
	if !errors.As(got, &ue) {
		t.Fatal("wrapped: *url.Error lost")
	}
	if strings.Contains(ue.Error(), "SEC-") {
		t.Fatalf("wrapped: inner url.Error not rewritten: %q", ue)
	}
}
