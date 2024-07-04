package tracker

import (
	"bytes"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"trackerr/internal/model/proxy"
)

const (
	PublicityPrivate = "private"
	PublicitySemi    = "semi"
	PublicityPublic  = "public"
)

type TrackerConfig struct {
	Rutracker Tracker
	Kinozal   Tracker
	Rutor     Tracker
	TeamHD    Tracker
	NNM       Tracker
}

type Tracker struct {
	TypeID      int
	Name        string
	Domain      string
	Credentials string
	ProxyType   proxy.ProxyType
	Publicity   string
	LoginURL    string
	DownloadURL string
	HourLimit   int
	Cookies     []*http.Cookie
	Exists      bool
}

func (t *Tracker) GetName() (name string) {
	return t.Name
}

func (t *Tracker) GetHost() (host string) {
	return "https://" + t.Domain
}

func (t *Tracker) GetProxyType() (proxyType proxy.ProxyType) {
	return t.ProxyType
}

func (t *Tracker) GetPublicity() (publicity string) {
	return t.Publicity
}

func (t *Tracker) GetHourLimit() (limit int) {
	return t.HourLimit
}

func (t *Tracker) GetCookies() (cookies []*http.Cookie) {
	return t.Cookies
}

func (t *Tracker) GetCredentials() (credentials string) {
	return t.Credentials
}

func (t *Tracker) LoginLink() (url string) {
	return t.LoginURL
}

func (t *Tracker) DownloadLink(topicID string) (url string) {
	// TODO: parse downloadLink from page (nnm)
	return fmt.Sprint(t.DownloadURL, topicID)
}

func (t *Tracker) Detect(url string) bool {
	rx := regexp.MustCompile("(" + t.Domain + ")")
	return rx.MatchString(url)
}

func (t *Tracker) Headers() (header *http.Header) {
	header = &http.Header{}
	return
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	return
}
func (t *Tracker) ParseDownloadID(b *bytes.Buffer, encoded bool) (downloadID string) {
	return
}
