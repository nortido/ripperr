package rutracker

import (
	"bytes"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"

	"torr/internal/helper"
)

const (
	Name        = "rutracker"
	Domain      = "rutracker.org"
	Host        = "https://" + Domain
	LoginURL    = Host + "/forum/login.php"
	DownloadURL = Host + "/forum/dl.php?t="
	TopicURL    = Host + "/forum/viewtopic.php?t="
)

type Tracker struct {
	// TODO: public type without credentials
}

func (t Tracker) Host() (host string) {
	return Host
}

func (t Tracker) LoginData() (link string, data *strings.Reader) {
	// TODO: Put credentials into envs
	form := url.Values{
		"login_username": {"johan01"},
		"login_password": {"dbhnejp"},
		"login":          {"вход"},
	}
	link = LoginURL
	data = strings.NewReader(form.Encode())

	return
}

func (t Tracker) DownloadLink(topicID string) (url string) {
	return fmt.Sprint(DownloadURL, topicID)
}

func (t Tracker) TopicLink(topicID string) (url string) {
	return fmt.Sprint(TopicURL, topicID)
}

func (t Tracker) Header() (header *http.Header) {
	header = &http.Header{}

	header.Add("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	//header.Add("Accept-Encoding", "gzip, deflate, br, zstd")
	header.Add("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	header.Add("Cache-Control", "no-cache")
	header.Add("Content-Type", "text/html; charset=cp1251")
	header.Add("Pragma", "no-cache")
	header.Add("Priority", "u=0, i")
	header.Add("Host", Host)
	header.Add("Referer", TopicURL+fmt.Sprint(rand.Intn(2000)+50))
	header.Add("Sec-Fetch-Dest", "document")
	header.Add("Sec-Fetch-Mode", "navigate")
	header.Add("Sec-Fetch-Site", "same-origin")
	header.Add("Sec-Fetch-User", "?1")
	header.Add("Upgrade-Insecure-Requests", "1")
	header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	header.Add("sec-ch-ua", `"Google Chrome";v="125", "Chromium";v="125", "Not.A/Brand";v="24"`)
	header.Add("sec-ch-ua-mobile", "?0")
	header.Add("sec-ch-ua-platform", `"macOS"`)
	//header.Add("Cookie", "bb_ssl=1; bb_guid=PLACEHOLDER; bb_t=PLACEHOLDER; bb_session=PLACEHOLDER; cf_clearance=PLACEHOLDER")

	return
}

func (t Tracker) Datetime(b *bytes.Buffer, encoded bool) (result *time.Time) {
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
