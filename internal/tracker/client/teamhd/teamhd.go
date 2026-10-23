package teamhd

import (
	"bytes"
	"net/http"
	"regexp"
	"time"

	"ripperr/internal/model/tracker"
)

var mskZone = time.FixedZone("MSK", 3*60*60) // даты на странице московские, летнего времени нет

type Tracker struct {
	tracker.Tracker
}

func (t *Tracker) Headers() (header *http.Header) {
	header = &http.Header{}

	header.Add("Content-Type", "text/html; charset=UTF-8")
	header.Add("Host", t.GetHost())

	return
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	var (
		matches []string
		rx      *regexp.Regexp
		r       time.Time
	)
	rx = regexp.MustCompile(`<b>Добавлен</b> - (\d{2}-\d{2}-\d{4} \d{2}:\d{2}:\d{2})<br />`)
	matches = rx.FindStringSubmatch(b.String())

	if len(matches) <= 1 {
		return
	}
	r, _ = time.ParseInLocation("02-01-2006 15:04:05", matches[1], mskZone)
	result = &r

	return
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
