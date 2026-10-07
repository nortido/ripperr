package tracker

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/model/proxy"
)

const (
	PublicityPublic = "public"
)

type TrackerConfig map[string]Tracker

type ITracker interface {
	// лежит тут, потому что реестр его знает, а пакеты трекеров импортируют реестр.
	// В tracker/client вышел бы цикл импортов
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
	// хуки прямо в ITracker: обертки (клиент, domainOverride) прячут тип трекера,
	// и проверка на отдельный интерфейс до него не достает
	PickTorrent(b *bytes.Buffer, encoded bool, held Held) (pick Pick)
	PageLink(link string) (page string)
	// торрент в клиенте без комментария трекер ищет сам по хешу
	AdoptLookup(name, hash string) (lookup string)
	AdoptURL(b *bytes.Buffer) (url string)
}

type Held struct {
	URL       string
	TopicID   string
	Stored    bool
	Hash      string
	Name      string
	Timestamp *time.Time
	// хеши других торрентов клиента с тем же адресом релиза: сохраненный TopicID мог записать любой из них
	Siblings []string
}

type Pick struct {
	DownloadID string
	Date       *time.Time
	Miss       string
}

type Tracker struct {
	TypeID       int
	Name         string
	Domain       string
	ActiveDomain string
	Mirrors      []string
	Retired      []string
	Unconfirmed  []string
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

func (t *Tracker) GetUnconfirmed() []string {
	return t.Unconfirmed
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
	out := make([]string, 0, 2+len(t.Mirrors)+len(t.Retired))
	if d := strings.TrimSpace(t.Domain); d != "" {
		out = append(out, d)
	}
	// убранный хост остается для сопоставления: комментарии старых торрентов несут его
	for _, m := range slices.Concat(t.Mirrors, t.Retired) {
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

func (t *Tracker) PickTorrent(b *bytes.Buffer, encoded bool, held Held) (pick Pick) {
	return
}

func (t *Tracker) PageLink(link string) (page string) {
	return link
}

func (t *Tracker) AdoptLookup(name, hash string) (lookup string) {
	return
}

func (t *Tracker) AdoptURL(b *bytes.Buffer) (url string) {
	return
}

func (t *Tracker) ParseMagnet(b *bytes.Buffer, encoded bool) (magnet string) {
	return
}

var (
	rxPostImg      = regexp.MustCompile(`<(?:var|img)[^>]*postImg[^>]*>`)
	rxPostImgTitle = regexp.MustCompile(`[\s"]title="(https?://[^"]+)"`)
	rxPostImgSrc   = regexp.MustCompile(`[\s"]src="(https?://[^"]+)"`)
)

func (t *Tracker) ParsePoster(b *bytes.Buffer, encoded bool) (poster string) {
	for _, tag := range rxPostImg.FindAllString(helper.PageText(b, encoded), -1) {
		// после FlareSolverr в src бывает заглушка битой картинки, живой адрес остается в title
		matches := rxPostImgTitle.FindStringSubmatch(tag)
		if len(matches) <= 1 {
			matches = rxPostImgSrc.FindStringSubmatch(tag)
		}
		if len(matches) <= 1 {
			continue
		}
		link := html.UnescapeString(matches[1])
		// в TorrentPier прижатая вправо картинка это постер, остальные скриншоты
		if strings.Contains(tag, "postImgAligned") {
			return link
		}
		if poster == "" {
			poster = link
		}
	}
	return
}

func AbsoluteURL(host, src string) string {
	// Telegram качает постер сам и понимает только абсолютную ссылку
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
