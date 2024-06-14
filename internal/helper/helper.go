package helper

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
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

func GetTimestamp(buf *bytes.Buffer) (ts int64, err error) {
	regex := regexp.MustCompile(`creation datei(\d{8,10})e`)
	match := regex.FindString(Substr(buf.String(), 100, 1000))

	if match == "" {
		err = fmt.Errorf("timestamp not found")
		return
	}
	var t int
	t, err = strconv.Atoi(match)
	ts = int64(t)
	return
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
