package rutracker

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
)

const (
	Name        = "rutracker"
	Domain      = "rutracker.org"
	Host        = "https://" + Domain
	LoginURL    = Host + "/forum/login.php"
	DownloadURL = Host + "/forum/dl.php?t="
)

type Tracker struct {
	// TODO: public type without credentials
}

func (t Tracker) Host() (host string) {
	return Host
}

func (t Tracker) LoginData() (link string, credentials *url.Values, cookies []*http.Cookie) {
	// TODO: Put credentials into envs|DB
	credentials = &url.Values{
		"login_username": {"johan01"},
		"login_password": {"dbhnejp"},
		"login":          {"вход"},
	}
	link = LoginURL

	return
}

func (t Tracker) DownloadLink(topicID string) (url string) {
	return fmt.Sprint(DownloadURL, topicID)
}

func (t Tracker) Header() (header *http.Header) {
	header = &http.Header{}

	header.Add("Content-Type", "text/html; charset=cp1251")
	header.Add("Host", Host)
	//header.Add("Cookie", "bb_ssl=1; bb_guid=PLACEHOLDER; bb_t=PLACEHOLDER; bb_session=PLACEHOLDER; cf_clearance=PLACEHOLDER")

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

func (t Tracker) ProxyType() (proxyType proxy.ProxyType) {
	return proxy.TypeFlare
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
