package helper

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
)

func Substr(s string, pos, length int) string {
	runes := []rune(s)
	l := pos + length
	if l > len(runes) {
		l = len(runes)
	}
	return string(runes[pos:l])
}

func GetTimestamp(buf *bytes.Buffer) (ts int, err error) {
	regex := regexp.MustCompile(`creation datei(\d{8,10})e`)
	match := regex.FindString(Substr(buf.String(), 100, 1000))

	if match == "" {
		err = fmt.Errorf("timestamp not found")
		return
	}
	ts, err = strconv.Atoi(match)
	return
}
