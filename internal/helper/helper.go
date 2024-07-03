package helper

import (
	"fmt"
	"strings"
	"time"
)

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
	r, _ := time.Parse(timeformat, t)
	result = &r

	return
}
