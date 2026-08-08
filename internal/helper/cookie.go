package helper

import (
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"
)

func CookieDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return ""
	}
	if !strings.HasPrefix(domain, ".") {
		return "." + domain
	}
	return domain
}

func EnrichCookies(cookies []*http.Cookie, domain string, ttl time.Duration) {
	d := CookieDomain(domain)
	expires := time.Now().Add(ttl)
	for _, c := range cookies {
		if c == nil {
			continue
		}
		if d != "" {
			c.Domain = d
		}
		c.Expires = expires
	}
}

func ParseCookies(line string) (cookies []*http.Cookie) {
	var part string

	if len(line) == 0 {
		return
	}
	line = textproto.TrimString(line)

	for len(line) > 0 { // continue since we have rest
		part, line, _ = strings.Cut(line, ";")
		part = textproto.TrimString(part)
		if part == "" {
			continue
		}
		name, val, _ := strings.Cut(part, "=")
		name = textproto.TrimString(name)
		if !isCookieNameValid(name) {
			continue
		}
		val, ok := parseCookieValue(val, true)
		if !ok {
			continue
		}
		cookies = append(cookies, &http.Cookie{Name: name, Value: val})
	}
	return
}

func CloneCookies(cookies []*http.Cookie) []*http.Cookie {
	if len(cookies) == 0 {
		return nil
	}
	out := make([]*http.Cookie, 0, len(cookies))
	for _, c := range cookies {
		if c == nil {
			continue
		}
		clone := *c
		out = append(out, &clone)
	}
	return out
}

// MergeCookies merges cookie lists; later values override earlier ones by name.
func MergeCookies(base, extra []*http.Cookie) []*http.Cookie {
	var (
		order []string
		index = make(map[string]*http.Cookie, len(base)+len(extra))
	)
	add := func(list []*http.Cookie) {
		for _, c := range list {
			if c == nil || c.Name == "" {
				continue
			}
			if _, seen := index[c.Name]; !seen {
				order = append(order, c.Name)
			}
			index[c.Name] = c
		}
	}
	add(base)
	add(extra)
	out := make([]*http.Cookie, 0, len(order))
	for _, name := range order {
		out = append(out, index[name])
	}
	return out
}

func CookiesString(ck []*http.Cookie) string {
	var b strings.Builder
	first := true
	for _, c := range ck {
		if c == nil || c.Name == "" {
			continue
		}
		if !first {
			b.WriteString("; ")
		}
		first = false
		b.WriteString(c.Name)
		b.WriteString("=")
		b.WriteString(c.Value)
	}
	return b.String()
}

func isCookieNameValid(raw string) bool {
	if raw == "" {
		return false
	}
	return strings.IndexFunc(raw, isNotToken) < 0
}

func isNotToken(r rune) bool {
	return !httpguts.IsTokenRune(r)
}

func parseCookieValue(raw string, allowDoubleQuote bool) (string, bool) {
	// Strip the quotes, if present.
	if allowDoubleQuote && len(raw) > 1 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	for i := 0; i < len(raw); i++ {
		if !validCookieValueByte(raw[i]) {
			return "", false
		}
	}
	return raw, true
}

func validCookieValueByte(b byte) bool {
	return 0x20 <= b && b < 0x7f && b != '"' && b != ';' && b != '\\'
}
