package kinozal

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
	header.Add("Referer", t.GetHost()+"/browse.php")
	//header.Add("Cookie", "uid=PLACEHOLDER; pass=PLACEHOLDER")

	return
}

var kinozalDatePatterns = []*regexp.Regexp{
	regexp.MustCompile(`<li>Обновлен<span class="floatright green n">([^<]+?) в (\d{1,2}:\d{2})</span>`),
	regexp.MustCompile(`<div class="bx1 justify"><b>Торрент-файл обновлен ([^<]+?) в (\d{1,2}:\d{2})</b>`),
	regexp.MustCompile(`<li>Залит<span class="floatright green n">([^<]+?) в (\d{1,2}:\d{2})</span>`),
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	text := helper.PageText(b, encoded)
	var matches []string
	for _, rx := range kinozalDatePatterns {
		if matches = rx.FindStringSubmatch(text); len(matches) > 1 {
			break
		}
	}
	if len(matches) <= 1 {
		return
	}
	for i, v := range []string{"сегодня", "вчера"} {
		if matches[1] == v {
			matches[1] = time.Now().AddDate(0, 0, -i).Format("2 01 2006")
		}
	}
	return helper.TimeFromRusDate(matches[1]+" "+matches[2], "2 01 2006 15:04")
}

func (t *Tracker) PostLoginURL() string {
	return t.GetHost() + "/browse.php"
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
