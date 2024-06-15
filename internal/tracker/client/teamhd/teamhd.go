package teamhd

import (
	"bytes"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
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

func (t Tracker) LoginData() (link string, data *strings.Reader) {
	form := url.Values{
		"username":             {"nortido"},
		"password":             {"dbhnejp1"},
		"g-recaptcha-response": {"03AFcWeA6i5c7mgZ6ghDihHyrQe1rkRgyjar9FifN_QWST0DYvsMrx0ipyXkptFTe7XNcg4x2-n1sBcD_ohce6cF0R3XW5gG6SVcLEgf5_0psjnsgZQPDExgTNgBwKcJZm23H3kMzsG8EygztFMvh4QL8AMw-7xZkiHBGclSJyA9b-FLqSawYM0T7CsEnk6lBZtwaHxFj19r9HKf0HIndR1jAjJOolI3ESj_52Rzg4jYzhcQNjDFJstwGyMczJX7zWK-FYIqBiOnUrBnPNVUdjZg60XfiwsrD2PoTA1qXYeUifd9oncVPbVaD7AbEXgrSdKTqTM5THcCJKuEqeoqndMN6nP0V-WrUW_6oYWHYrH6yc-q7yBW6g7oZXLNrXa3jEXh3pAuAXBuNSkGEobrhTocUy4Nad10w2gGMprfL6S-tElzcEmSuNnk7gTH1kQDPNO2xzqHhPpZwKJAuqJeyhY_xZTCdoosxf3gOJEy3OhiDhqA8rurcbbxjtPG7lA-jbVHQMj90cjFbJQdmDLde2PTZPXj6JKtnpszR6TAnSq1V9nMGuwFThvJh0A3Nlqa857lpExpAgmBA6nWpIxS_ysrg27CX6F766gIZa4mylH7CVS7c4mE4X5pVeHjR7XjjZpTWfeNsobZUfa9mMWKEBriyqivn7JmT8Ndx_Hgn6pNCCz6B7i1yqP2Q"},
		"returnto":             {""},
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

	return
}

func (t Tracker) Datetime(b *bytes.Buffer, encoded bool) (result *time.Time) {
	var (
		bb      []byte
		matches []string
		rx      *regexp.Regexp
		r       time.Time
	)
	bb = b.Bytes()
	rx = regexp.MustCompile(`<b>Добавлен</b> - (.+)<br>`)
	matches = rx.FindStringSubmatch(string(bb))

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
