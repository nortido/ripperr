package toloka

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
	_ "time/tzdata"

	"ripperr/internal/model/tracker"
)

var (
	// разделы закрыты для гостей, ссылку и дату регистрации взяли у marauder и
	// monitorrent. Вживую не проверяли
	rxDownloadID = regexp.MustCompile(`href="download\.php\?id=(\d+)"`)
	rxRegistered = regexp.MustCompile(`Зареєстрований:[\s\S]{0,300}?(\d{4}-\d{2}-\d{2} \d{2}:\d{2})`)
	rxUserID     = regexp.MustCompile(`"userid";i:(-?\d+)`)
	rxLegacyHost = regexp.MustCompile(`(?i)^https?://toloka\.hurtom\.com(?::\d+)?(?:[/?#]|$)`)
)

// время киевское, с летним. Зона из встроенной базы, tzdata в образе не нужна
var kyivZone, kyivErr = time.LoadLocation("Europe/Kyiv")

type Tracker struct {
	tracker.Tracker
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	if kyivErr != nil {
		return
	}
	// страницы в UTF-8, helper.PageText с cp1251 испортил бы кириллицу
	matches := rxRegistered.FindStringSubmatch(b.String())
	if len(matches) <= 1 {
		return
	}
	r, err := time.ParseInLocation("2006-01-02 15:04", matches[1], kyivZone)
	if err != nil {
		return
	}
	return &r
}

func (t *Tracker) ParseDownloadID(b *bytes.Buffer, encoded bool) (downloadID string) {
	matches := rxDownloadID.FindStringSubmatch(b.String())
	if len(matches) <= 1 {
		return
	}
	return matches[1]
}

func (t *Tracker) PickTorrent(b *bytes.Buffer, encoded bool, _ tracker.Held) (pick tracker.Pick) {
	// у вложения свой номер, по номеру темы скачалось бы не то
	if pick.DownloadID = t.ParseDownloadID(b, encoded); pick.DownloadID == "" {
		pick.Miss = "toloka: на странице нет ссылки на .torrent"
	}
	return
}

func (t *Tracker) TopicLink(topicID string) (url string) {
	if topicID == "" {
		return
	}
	return fmt.Sprintf("%s/t%s", t.GetHost(), topicID)
}

func (t *Tracker) LoginAuthIncomplete(cookies []*http.Cookie) bool {
	// toloka_data это сериализованный PHP-массив. Куку ставят и гостю, но у него
	// userid -1, у вошедшего номер положительный
	for _, c := range cookies {
		if c == nil || c.Name != "toloka_data" {
			continue
		}
		value, err := url.QueryUnescape(c.Value)
		if err != nil {
			value = c.Value
		}
		matches := rxUserID.FindStringSubmatch(value)
		if len(matches) <= 1 {
			return true
		}
		id, err := strconv.Atoi(matches[1])
		return err != nil || id <= 0
	}
	return true
}

func (t *Tracker) LoginAuthHint() string {
	return "toloka_data cookie has no user id: check username and password"
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}

func (t *Tracker) Detect(link string) bool {
	return t.Tracker.Detect(link) || rxLegacyHost.MatchString(link)
}

func (t *Tracker) PageLink(link string) string {
	if !rxLegacyHost.MatchString(link) {
		return link
	}
	u, err := url.Parse(link)
	if err != nil {
		return link
	}
	u.Scheme = "https"
	u.Host = t.GetDomain()
	return u.String()
}
