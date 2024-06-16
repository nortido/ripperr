package teamhd

import (
	"bytes"
	"fmt"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"

	"torr/internal/model/proxy"
)

const (
	Name        = "teamhd"
	Domain      = "teamhd.org"
	Host        = "https://" + Domain
	LoginURL    = Host + "/takelogin.php"
	DownloadURL = Host + "/download.php?id="
	TopicURL    = Host + "/details/id"
)

type Tracker struct {
}

func (t Tracker) Host() (host string) {
	return Host
}

func (t Tracker) LoginData() (link string, data *strings.Reader, cookies []*http.Cookie) {
	link = DownloadURL
	// TODO: parse cookies from single string
	cookies = make([]*http.Cookie, 6)
	cookies = []*http.Cookie{
		{Name: "cross-site-cookie", Value: "https://teamhd.org"},
		{Name: "PHPSESSID", Value: "PLACEHOLDER"},
		{Name: "uid", Value: "6347"},
		{Name: "lang", Value: "russian"},
		{Name: "pass", Value: "PLACEHOLDER"},
		{Name: "token", Value: "PLACEHOLDER"},
	}
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

	return
}

func (t Tracker) Datetime(b *bytes.Buffer, encoded bool) (result *time.Time) {
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
