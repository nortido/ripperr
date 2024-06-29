package rutor

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"trackerr/internal/model/proxy"
	"trackerr/internal/model/tracker"
)

const (
	Name        = "rutor"
	Domain      = "rutor.is"
	Host        = "https://" + Domain
	DownloadURL = "https://d.rutor.info/download/"
)

type Tracker struct {
}

func (t Tracker) Host() (host string) {
	return Host
}
func (t Tracker) LoginData() (link string, credentials *url.Values, cookies []*http.Cookie) {
	link = ""
	credentials = &url.Values{}

	return
}

func (t Tracker) DownloadLink(topicID string) (url string) {
	return fmt.Sprint(DownloadURL, topicID)
}

func (t Tracker) Header() (header *http.Header) {
	header = &http.Header{}

	header.Add("Host", Host)
	header.Add("Referer", "https://rutor.is/browse/")

	return
}

func (t Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	return &time.Time{}
}

func (t Tracker) ProxyType() (proxyType proxy.ProxyType) {
	return proxy.TypeNone
}

func (t Tracker) Publicity() (publicity string) {
	return tracker.PublicityPublic
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
