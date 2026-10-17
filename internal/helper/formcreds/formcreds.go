package formcreds

import (
	"net/url"
	"strings"
)

func Get(raw, field string) string {
	for _, part := range strings.Split(raw, "&") {
		name, value, ok := strings.Cut(part, "=")
		if !ok || name != field {
			continue
		}
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			// битую экранировку отдаем как есть, иначе правка логина сотрет
			// то, что пользователь набрал руками
			return value
		}
		return decoded
	}
	return ""
}

func Set(raw, field, value string) string {
	encoded := field + "=" + url.QueryEscape(value)
	if raw == "" {
		return encoded
	}
	// строку не пересобираем: у каждого трекера там свои служебные поля (login=вход, to=/),
	// их порядок и состав должны остатся как были
	parts := strings.Split(raw, "&")
	for i, part := range parts {
		name, _, ok := strings.Cut(part, "=")
		if !ok || name != field {
			continue
		}
		parts[i] = encoded
		return strings.Join(parts, "&")
	}
	return raw + "&" + encoded
}

const (
	LoginPlaceholder    = "{login}"
	PasswordPlaceholder = "{password}"
)

func Expand(body, login, password string) string {
	// один проход: значение, содержащее плейсхолдер, не подставится повторно
	return strings.NewReplacer(
		LoginPlaceholder, url.QueryEscape(login),
		PasswordPlaceholder, url.QueryEscape(password),
	).Replace(body)
}

func Templatize(body, userField, passField string) (tmpl, login, password string, hasLogin, hasPassword bool) {
	parts := strings.Split(body, "&")
	var foundLogin, foundPass bool
	for i, part := range parts {
		name, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch name {
		case userField:
			foundLogin = true
			parts[i] = userField + "=" + LoginPlaceholder
			if !hasLogin && value != "" && value != LoginPlaceholder {
				login, hasLogin = unescapeOrRaw(value), true
			}
		case passField:
			foundPass = true
			parts[i] = passField + "=" + PasswordPlaceholder
			if !hasPassword && value != "" && value != PasswordPlaceholder {
				password, hasPassword = unescapeOrRaw(value), true
			}
		}
	}
	tmpl = strings.Join(parts, "&")
	if !foundLogin && !strings.Contains(body, LoginPlaceholder) {
		tmpl = appendPart(tmpl, userField+"="+LoginPlaceholder)
	}
	if !foundPass && !strings.Contains(body, PasswordPlaceholder) {
		tmpl = appendPart(tmpl, passField+"="+PasswordPlaceholder)
	}
	return tmpl, login, password, hasLogin, hasPassword
}

func appendPart(raw, part string) string {
	if raw == "" {
		return part
	}
	return raw + "&" + part
}

func unescapeOrRaw(value string) string {
	decoded, err := url.QueryUnescape(value)
	if err != nil {
		return value
	}
	return decoded
}
