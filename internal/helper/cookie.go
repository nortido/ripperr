package helper

import (
	"net/http"
	"strings"
	"time"
)

func cookieDomain(domain string) string {
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
	d := cookieDomain(domain)
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
	// куки набирают руками в настройках, а http.ParseCookie на первой паре без "="
	// (хоть на висящей в конце ";") отдает ошибку и ни одной куки. Поэтому режем
	// сами и мусор пропускаем, имя и значение все равно проверяет она
	for _, part := range strings.Split(line, ";") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		if parsed, err := http.ParseCookie(part); err == nil {
			cookies = append(cookies, parsed...)
		}
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
