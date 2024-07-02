package nnm

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"golang.org/x/text/encoding/charmap"

	"trackerr/internal/helper"
	"trackerr/internal/model/proxy"
	"trackerr/internal/model/tracker"
)

const (
	Name        = "nnm"
	Domain      = "nnmclub.to"
	Host        = "https://" + Domain
	LoginURL    = Host + "/forum/login.php"
	DownloadURL = Host + "/forum/download.php?id="
)

type Tracker struct {
}

func (t Tracker) Host() (host string) {
	return Host
}

func (t Tracker) LoginData() (link string, credentials *url.Values, cookies []*http.Cookie) {
	credentials = &url.Values{
		"username":  {"johan90"},
		"password":  {"dbhnejp"},
		"autologin": {"on"},
		"redirect":  {""},
		"code":      {"58c36bdbff5c5639"},
		"login":     {"%C2%F5%EE%E4"},
	}
	link = LoginURL

	return
}

func (t Tracker) DownloadLink(topicID string) (url string) {
	return fmt.Sprint(DownloadURL, topicID)
}

func (t Tracker) Header() (header *http.Header) {
	header = &http.Header{}

	header.Add("Content-Type", "text/html; charset=windows-1251")
	header.Add("Host", Host)

	return
}

func (t Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
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

func (t Tracker) ProxyType() (proxyType proxy.ProxyType) {
	return proxy.TypeFlare
}

func (t Tracker) HourLimit() (limit int) {
	return 20
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
