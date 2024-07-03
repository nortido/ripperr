package kinozal

import (
	"bytes"
	"net/http"
	"regexp"
	"time"

	"golang.org/x/text/encoding/charmap"

	"trackerr/internal/helper"
	"trackerr/internal/model/tracker"
)

type Tracker struct {
	tracker.Tracker
}

func (t *Tracker) Header() (header *http.Header) {
	header = &http.Header{}

	header.Add("Content-Type", "text/html; charset=windows-1251")
	header.Add("Host", t.GetHost())
	header.Add("Referer", "https://kinozal.tv/browse.php")
	//header.Add("Cookie", "uid=PLACEHOLDER; pass=PLACEHOLDER")

	return
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	var (
		i       int
		v       string
		bb      []byte
		rx      *regexp.Regexp
		matches []string
	)
	if !encoded {
		bb, _ = charmap.Windows1251.NewDecoder().Bytes(b.Bytes())
	} else {
		bb = b.Bytes()
	}
	rx = regexp.MustCompile(`<div class="bx1 justify"><b>Торрент-файл обновлен (.+) в (.+)</b>`)
	matches = rx.FindStringSubmatch(string(bb))
	if len(matches) <= 1 {
		rx = regexp.MustCompile(`<li>Залит<span class="floatright green n">(.+) в (.+)</span>`)
		matches = rx.FindStringSubmatch(string(bb))
	}
	if len(matches) <= 1 {
		return
	}
	for i, v = range []string{"сегодня", "вчера"} {
		if matches[1] == v {
			matches[1] = time.Now().AddDate(0, 0, -i).Format("2 01 2006")
		}
	}
	result = helper.TimeFromRusDate(matches[1]+" "+matches[2], "2 01 2006 15:04")

	return
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
