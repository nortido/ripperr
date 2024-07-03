package rutracker

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

func (t *Tracker) Headers() (header *http.Header) {
	header = &http.Header{}

	header.Add("Content-Type", "text/html; charset=cp1251")
	header.Add("Host", t.GetHost())
	//header.Add("Cookie", "bb_ssl=1; bb_guid=PLACEHOLDER; bb_t=PLACEHOLDER; bb_session=PLACEHOLDER; cf_clearance=PLACEHOLDER")

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
	rx = regexp.MustCompile(`<span class="posted_since hide-for-print">\(.+, ред\. (.+)\)</span>`)
	matches = rx.FindStringSubmatch(string(bb))

	if len(matches) <= 1 {
		rx = regexp.MustCompile(`<a class="p-link small" href="viewtopic\.php\?t=\d+">(.+)</a>`)
		matches = rx.FindStringSubmatch(string(bb))
	}
	if len(matches) <= 1 {
		return
	}
	result = helper.TimeFromRusDate(matches[1], "02-01-06 15:04")

	return
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
