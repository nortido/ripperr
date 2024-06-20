package helper

import (
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"
)

func ParseCookies(line string, filter string) (cookies []*http.Cookie) {
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
		if filter != "" && filter != name {
			continue
		}
		val, ok := parseCookieValue(val, true)
		if !ok {
			continue
		}
		cookies = append(cookies, &http.Cookie{Name: name, Value: val, Expires: time.Now().AddDate(0, 0, 30)})
	}
	return
}

func CookiesString(ck []*http.Cookie) (res string) {
	for _, c := range ck {
		res += c.Name + "=" + c.Value + "; "
	}
	return
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
