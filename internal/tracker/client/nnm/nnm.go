package nnm

import (
	"bytes"
	"net/http"
	"regexp"
	"time"

	"trackerr/internal/helper"
	"trackerr/internal/model/tracker"
)

type Tracker struct {
	tracker.Tracker
}

func (t *Tracker) Headers() (header *http.Header) {
	header = &http.Header{}

	header.Add("Content-Type", "text/html; charset=windows-1251")
	header.Add("Host", t.GetHost())

	return
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	var (
		rx      *regexp.Regexp
		matches []string
	)
	rx = regexp.MustCompile(`Зарегистрирован:&nbsp;</td>\n\s+<td class="genmed">&nbsp;(\d{2} [А-я]{3} \d{4} \d{2}:\d{2}:\d{2})</td>`)
	matches = rx.FindStringSubmatch(helper.PageText(b, encoded))

	if len(matches) <= 1 {
		return
	}
	result = helper.TimeFromRusDate(matches[1], "02 01 2006 15:04:05")

	return
}

func (t *Tracker) ParseDownloadID(b *bytes.Buffer, encoded bool) (downloadID string) {
	var (
		rx      *regexp.Regexp
		matches []string
	)
	rx = regexp.MustCompile(`<a href="download.php\?id=(\d+)" rel="nofollow">Скачать</a>`)
	matches = rx.FindStringSubmatch(helper.PageText(b, encoded))

	if len(matches) <= 1 {
		return
	}
	downloadID = matches[1]

	return
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
