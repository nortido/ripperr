package rutor

import (
	"bytes"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	Name        = "rutor"
	Domain      = "rutor.is"
	Host        = "https://" + Domain
	DownloadURL = "https://d.rutor.info/download/"
	TopicURL    = "http://" + Domain + "/torrent/"
)

type Tracker struct {
}

func (t Tracker) Host() (host string) {
	return Host
}
func (t Tracker) LoginData() (link string, data *strings.Reader) {
	link = Host

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
	header.Add("Host", Host)
	header.Add("Pragma", "no-cache")
	header.Add("Referer", "https://rutor.is/browse/")
	header.Add("Sec-Fetch-Dest", "document")
	header.Add("Sec-Fetch-Mode", "navigate")
	header.Add("Sec-Fetch-Site", "none")
	header.Add("Sec-Fetch-User", "?1")
	header.Add("Upgrade-Insecure-Requests", "1")
	header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	header.Add("sec-ch-ua", `"Google Chrome";v="125", "Chromium";v="125", "Not.A/Brand";v="24"`)
	header.Add("sec-ch-ua-mobile", "?0")
	header.Add("sec-ch-ua-platform", `"macOS"`)

	return
}

func (t Tracker) Datetime(b *bytes.Buffer, encoded bool) (result *time.Time) {
	var futureTime time.Time
	futureTime = time.Now().AddDate(0, 0, 1)

	return &futureTime
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
