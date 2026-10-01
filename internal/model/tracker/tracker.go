package tracker

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/model/proxy"
)

const (
	PublicityPublic = "public"
)

// TrackerConfig это настройки трекеров по ключу реестра; состав набора задаёт
// реестр, а не этот тип, поэтому новый трекер тут ничего не меняет.
type TrackerConfig map[string]Tracker

// ITracker это всё, что ядро спрашивает у трекера. Интерфейс живёт здесь, а не в
// tracker/client: реестр обязан знать его, а пакеты трекеров импортируют реестр,
// и объявление в client замкнуло бы импорты в кольцо.
type ITracker interface {
	GetName() (name string)
	GetDomain() (host string)
	GetHost() (host string)
	GetProxyType() (proxyType proxy.ProxyType)
	GetPublicity() (publicity string)
	GetHourLimit() (limit int)
	GetCookies() (cookies []*http.Cookie)
	GetCredentials() (credentials string)
	GetMirrors() []string
	LoginLink() (url string)
	DownloadLink(topicID string) (url string)
	TopicLink(topicID string) (url string)
	Detect(url string) bool
	Headers() (header *http.Header)
	ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time)
	ParseDownloadID(b *bytes.Buffer, encoded bool) (downloadID string)
	ParseMagnet(b *bytes.Buffer, encoded bool) (magnet string)
	ParsePoster(b *bytes.Buffer, encoded bool) (poster string)
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

// разметка TorrentPier (rutracker, nnm, teamhd) помечает картинки поста
// классом postImg: прижатая к правому краю это постер релиза, остальные в теле
// поста это скриншоты. Ссылка лежит в title у <var> и в src у уже отрисованного
// <img>, поэтому берём оба варианта
var (
	rxPostImg    = regexp.MustCompile(`<(?:var|img)[^>]*postImg[^>]*>`)
	rxPostImgURL = regexp.MustCompile(`[\s"](?:title|src)="(https?://[^"]+)"`)
)

func (t *Tracker) ParsePoster(b *bytes.Buffer, encoded bool) (poster string) {
	for _, tag := range rxPostImg.FindAllString(helper.PageText(b, encoded), -1) {
		matches := rxPostImgURL.FindStringSubmatch(tag)
		if len(matches) <= 1 {
			continue
		}
		link := html.UnescapeString(matches[1])
		if strings.Contains(tag, "postImgAligned") {
			return link
		}
		if poster == "" {
			poster = link
		}
	}
	return
}

// в разметке трекеров адрес картинки бывает и относительным, и без схемы, а
// Telegram скачивает постер сам и понимает только абсолютную ссылку
func AbsoluteURL(host, src string) string {
	switch {
	case src == "", strings.HasPrefix(src, "http"):
		return src
	case strings.HasPrefix(src, "//"):
		return "https:" + src
	case strings.HasPrefix(src, "/"):
		return strings.TrimSuffix(host, "/") + src
	}
	return strings.TrimSuffix(host, "/") + "/" + src
}
