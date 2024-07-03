package nnm

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

	return
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	var (
		bb      []byte
		rx      *regexp.Regexp
		matches []string
	)
	if !encoded {
		bb, _ = charmap.Windows1251.NewDecoder().Bytes(b.Bytes())
	} else {
		bb = b.Bytes()
	}
	rx = regexp.MustCompile(`Зарегистрирован:&nbsp;</td>\n\s+<td class="genmed">&nbsp;(\d{2} [А-я]{3} \d{4} \d{2}:\d{2}:\d{2})</td>`)
	matches = rx.FindStringSubmatch(string(bb))

	if len(matches) <= 1 {
		return
	}
	result = helper.TimeFromRusDate(matches[1], "02 01 2006 15:04:05")

	return
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
