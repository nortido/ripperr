package teamhd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"trackerr/internal/helper"
	"trackerr/internal/model/proxy"
	"trackerr/internal/model/tracker"
)

const (
	Name        = "teamhd"
	Domain      = "teamhd.org"
	Host        = "https://" + Domain
	DownloadURL = Host + "/download.php?id="
)

type Tracker struct {
}

func (t Tracker) Host() (host string) {
	return Host
}

func (t Tracker) LoginData() (link string, credentials *url.Values, cookies []*http.Cookie) {
	link = ""
	credentials = &url.Values{}
	cookies = helper.ParseCookies("cross-site-cookie=https://teamhd.org; PHPSESSID=PLACEHOLDER; uid=PLACEHOLDER; lang=russian; pass=PLACEHOLDER; token=PLACEHOLDER", "")

	return
}

func (t Tracker) DownloadLink(topicID string) (url string) {
	return fmt.Sprint(DownloadURL, topicID)
}

func (t Tracker) Header() (header *http.Header) {
	header = &http.Header{}

	header.Add("Content-Type", "text/html; charset=UTF-8")
	header.Add("Host", Host)

	return
}

func (t Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
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
	r, _ = time.Parse("02-01-2006 15:04:05", matches[1])
	result = &r

	return
}

func (t Tracker) ProxyType() (proxyType proxy.ProxyType) {
	return proxy.TypeNone
}

func (t Tracker) HourLimit() (limit int) {
	return 25
}

func (t Tracker) Publicity() (publicity string) {
	return tracker.PublicityPrivate
}

func (t Tracker) Detect(url string) bool {
	rx := regexp.MustCompile("(" + Domain + ")")

	return rx.MatchString(url)
}

func (t Tracker) Name() (name string) {
	return Name
}

func New() *Tracker {
	return &Tracker{}
}
