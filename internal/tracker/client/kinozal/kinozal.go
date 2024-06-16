package kinozal

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"

	"torr/internal/helper"
	"torr/internal/model/proxy"
)

const (
	Name        = "kinozal"
	Domain      = "kinozal.tv"
	Host        = "https://" + Domain
	LoginURL    = Host + "/takelogin.php"
	DownloadURL = Host + "/download.php?id="
	TopicURL    = Host + "/details.php?id="
)

type Tracker struct {
}

func (t Tracker) Host() (host string) {
	return Host
}

func (t Tracker) LoginData() (link string, data *strings.Reader, cookies []*http.Cookie) {
	form := url.Values{
		"username": {"nortido"},
		"password": {"dbhnejp1"},
		"returnto": {""},
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
	header.Add("Connection", "keep-alive")
	header.Add("Content-Type", "text/html; charset=windows-1251")
	header.Add("Host", Host)
	header.Add("Pragma", "no-cache")
	header.Add("Priority", "u=0, i")
	header.Add("Referer", "https://kinozal.tv/browse.php")
	header.Add("Sec-Fetch-Dest", "document")
	header.Add("Sec-Fetch-Mode", "navigate")
	header.Add("Sec-Fetch-Site", "same-origin")
	header.Add("Sec-Fetch-User", "?1")
	header.Add("Upgrade-Insecure-Requests", "1")
	header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	header.Add("sec-ch-ua", `"Google Chrome";v="125", "Chromium";v="125", "Not.A/Brand";v="24"`)
	header.Add("sec-ch-ua-mobile", "?0")
	header.Add("sec-ch-ua-platform", `"macOS"`)
	//header.Add("Cookie", "uid=PLACEHOLDER; pass=PLACEHOLDER")

	return
}

func (t Tracker) Datetime(b *bytes.Buffer, encoded bool) (result *time.Time) {
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

func (t Tracker) ProxyType() (proxyType proxy.ProxyType) {
	return proxy.ProxyNone
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
