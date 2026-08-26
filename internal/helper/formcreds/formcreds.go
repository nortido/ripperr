package formcreds

import (
	"net/url"
	"strings"
)

// Пакет правит тело формы логина трекера (application/x-www-form-urlencoded),
// не пересобирая его: у каждого трекера в строке лежат свои служебные поля
// (login=вход, to=/), и порядок с составом должны пережить правку логина.

func Get(raw, field string) string {
	for _, part := range strings.Split(raw, "&") {
		name, value, ok := strings.Cut(part, "=")
		if !ok || name != field {
			continue
		}
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			// битую экранировку показываем как есть: иначе правка логина
			// стёрла бы значение, которое пользователь набрал руками
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
