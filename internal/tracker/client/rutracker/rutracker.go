package rutracker

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"time"

	"trackerr/internal/helper"
	"trackerr/internal/model/tracker"
)

var (
	rxPostedSince = regexp.MustCompile(`<span class="posted_since hide-for-print">\(.+, ред\. (.+)\)</span>`)
	rxPLink       = regexp.MustCompile(`<a class="p-link small" href="viewtopic\.php\?t=\d+">(.+)</a>`)
	rxMagnet      = regexp.MustCompile(`href="(magnet:\?[^"]+)"`)
)

type Tracker struct {
	tracker.Tracker
}

func (t *Tracker) Headers() (header *http.Header) {
	header = &http.Header{}

	header.Add("Content-Type", "text/html; charset=cp1251")
	header.Add("Host", t.GetHost())

	return
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	text := helper.PageText(b, encoded)
	matches := rxPostedSince.FindStringSubmatch(text)
	if len(matches) <= 1 {
		matches = rxPLink.FindStringSubmatch(text)
	}
	if len(matches) <= 1 {
		return
	}
	result = helper.TimeFromRusDate(matches[1], "02-01-06 15:04")

	return
}

func (t *Tracker) FlareSession() string {
	return t.GetName()
}

func (t *Tracker) PostLoginURL() string {
	return t.GetHost() + "/forum/index.php"
}

func (t *Tracker) TopicLink(topicID string) (url string) {
	if topicID == "" {
		return
	}
	return fmt.Sprintf("%s/forum/viewtopic.php?t=%s", t.GetHost(), topicID)
}

func (t *Tracker) ParseMagnet(b *bytes.Buffer, encoded bool) (magnet string) {
	matches := rxMagnet.FindStringSubmatch(helper.PageText(b, encoded))
	if len(matches) < 2 {
		return
	}
	return html.UnescapeString(matches[1])
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
