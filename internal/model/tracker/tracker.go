package tracker

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"trackerr/internal/model/proxy"
)

const (
	PublicityPublic = "public"
)

type TrackerConfig struct {
	Rutracker Tracker
	Kinozal   Tracker
	Rutor     Tracker
	TeamHD    Tracker
	NNM       Tracker
}

type Tracker struct {
	TypeID       int
	Name         string
	Domain       string
	ActiveDomain string
	Mirrors      []string
	Credentials  string
	ProxyType    proxy.ProxyType
	Publicity    string
	LoginURL     string
	DownloadURL  string
	HourLimit    int
	Cookies      []*http.Cookie
	Enabled      bool
	Exists       bool
}

func (t *Tracker) GetName() (name string) {
	return t.Name
}

func (t *Tracker) GetDomain() (host string) {
	return t.Domain
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
	return fmt.Sprint(t.DownloadURL, topicID)
}

func (t *Tracker) TopicLink(topicID string) (url string) {
	return
}

func (t *Tracker) GetMirrors() []string {
	return t.Mirrors
}

func (t *Tracker) GetActiveDomain() string {
	return t.ActiveDomain
}

func (t *Tracker) Detect(link string) bool {
	for _, d := range t.matchDomains() {
		if strings.Contains(link, d) {
			return true
		}
	}
	return false
}

func (t *Tracker) matchDomains() []string {
	out := make([]string, 0, 1+len(t.Mirrors))
	if d := strings.TrimSpace(t.Domain); d != "" {
		out = append(out, d)
	}
	for _, m := range t.Mirrors {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	if ad := strings.TrimSpace(t.ActiveDomain); ad != "" {
		out = append(out, ad)
	}
	return out
}

func ReplaceURLHost(rawURL, newHost string) string {
	if rawURL == "" || newHost == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Host = newHost
	return u.String()
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

func (t *Tracker) ParseMagnet(b *bytes.Buffer, encoded bool) (magnet string) {
	return
}
