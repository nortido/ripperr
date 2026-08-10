package helper

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// PageText decodes a tracker response body. Responses fetched directly are
// windows-1251 encoded; responses proxied through FlareSolverr arrive already
// decoded (encoded=true), so they're returned as-is.
func PageText(b *bytes.Buffer, encoded bool) string {
	if encoded {
		return b.String()
	}
	bb, _ := charmap.Windows1251.NewDecoder().Bytes(b.Bytes())
	return string(bb)
}

func Substr(s string, pos, length int) string {
	runes := []rune(s)
	l := pos + length
	if l > len(runes) {
		l = len(runes)
	}
	return string(runes[pos:l])
}

func TimeFromRusDate(t string, timeformat string) (result *time.Time) {
	var (
		i int
		v string
	)

	for i, v = range []string{"Янв", "Фев", "Мар", "Апр", "Май", "Июн", "Июл", "Авг", "Сен", "Окт", "Ноя", "Дек"} {
		t = strings.Replace(t, v, fmt.Sprintf("%02d", i+1), 1)
	}
	for i, v = range []string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"} {
		t = strings.Replace(t, v, fmt.Sprintf("%02d", i+1), 1)
	}
	r, err := time.Parse(timeformat, t)
	if err != nil {
		return nil
	}
	result = &r

	return
}

func InArray[T comparable](needle T, haystack []T) bool {
	for _, v := range haystack {
		if v == needle {
			return true
		}
	}
	return false
}
