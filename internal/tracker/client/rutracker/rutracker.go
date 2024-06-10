package rutracker

import (
	"fmt"
	"math/rand"
	"net/http"
	"regexp"
)

const (
	Domain      = "rutracker.org"
	Host        = "https://" + Domain
	DownloadURL = Host + "/forum/dl.php?t="
	TopicURL    = Host + "/forum/viewtopic.php?t="
)

type Tracker struct {
	version int
}

func (t Tracker) Host() (host string) {
	return Host
}

func (t Tracker) DownloadURL(topicID string) (url string) {
	return fmt.Sprint(DownloadURL, topicID)
}

func (t Tracker) TopicURL(topicID string) (url string) {
	return fmt.Sprint(TopicURL, topicID)
}

func (t Tracker) Header() (header *http.Header) {
	header = &http.Header{}

	header.Add("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	header.Add("Accept-Encoding", "gzip, deflate, br, zstd")
	header.Add("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	header.Add("Cache-Control", "no-cache")
	header.Add("Pragma", "no-cache")
	header.Add("Priority", "u=0, i")
	header.Add("Host", Host)
	header.Add("Referer", TopicURL+fmt.Sprint(rand.Intn(2000)+50))
	header.Add("Sec-Fetch-Dest", "document")
	header.Add("Sec-Fetch-Mode", "navigate")
	header.Add("Sec-Fetch-Site", "same-origin")
	header.Add("Sec-Fetch-User", "?1")
	header.Add("Upgrade-Insecure-Requests", "1")

	getDynHeader(t.version, header)

	if t.version > 2 {
		t.version = 0
	} else {
		t.version = t.version + 1
	}
	return
}

func (t Tracker) Detect(url string) bool {
	rx := regexp.MustCompile("(" + Domain + ")")

	return rx.MatchString(url)
}

func (t Tracker) Name() (name string) {
	return "rutracker"
}

func getDynHeader(version int, header *http.Header) {
	switch version {
	case 0:
		//header.Add("Cookie", "bb_t=PLACEHOLDER; bb_session=PLACEHOLDER; bb_ssl=1; bb_guid=PLACEHOLDER; cf_clearance=PLACEHOLDER")
		header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15")
	case 1:
		//header.Add("Cookie", "bb_guid=PLACEHOLDER; bb_ssl=1; bb_session=PLACEHOLDER; cf_clearance=PLACEHOLDER")
		header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 YaBrowser/23.7.0.2562 Yowser/2.5 Safari/537.36")
		header.Add("Sec-Ch-Ua", `"Not.A/Brand";v="8", "Chromium";v="114", "YaBrowser";v="23"`)
		header.Add("Sec-Ch-Ua-Mobile", "?0")
		header.Add("Sec-Ch-Ua-Platform", `"macOS"`)
	default:
		//header.Add("Cookie", "bb_ssl=1; bb_guid=PLACEHOLDER; bb_t=PLACEHOLDER; bb_session=PLACEHOLDER; cf_clearance=PLACEHOLDER")
		header.Add("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
		header.Add("sec-ch-ua", `"Google Chrome";v="125", "Chromium";v="125", "Not.A/Brand";v="24"`)
		header.Add("sec-ch-ua-mobile", "?0")
		header.Add("sec-ch-ua-platform", `"macOS"`)
	}
	return
}

func New() *Tracker {
	return &Tracker{}
}
