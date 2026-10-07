package riperam

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"ripperr/internal/model/tracker"
)

var (
	// гостю ссылку на вложение phpBB не отдают, разметку взяли у TorrentMonitor и
	// Prowlarr. Вживую не проверяли
	rxDownloadID = regexp.MustCompile(`download/file\.php\?id=(\d+)`)
	rxPoster     = regexp.MustCompile(`<a\s[^>]*href="([^"]+)"[^>]*rel="prettyPhotoPosters\[0\]"`)
	// вторая дата строки "Торрент", в скобках, как у TorrentMonitor. Какая из двух
	// дата обновления, неясно: на живой странице они совпадают
	rxUpdated = regexp.MustCompile(`Торрент</td>\s*<td[^>]*>[^<\[]*\[\s*(\d{1,2}) ([а-я]+) (\d{4}), (\d{1,2}):(\d{2})\s*\]`)
	rxUserCk  = regexp.MustCompile(`^phpbb_\d+_u$`)
)

var months = map[string]time.Month{
	"янв": time.January, "фев": time.February, "мар": time.March, "апр": time.April,
	"май": time.May, "мая": time.May, "июн": time.June, "июл": time.July,
	"авг": time.August, "сен": time.September, "окт": time.October,
	"ноя": time.November, "дек": time.December,
}

var mskZone = time.FixedZone("MSK", 3*60*60)

type Tracker struct {
	tracker.Tracker
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	// тут UTF-8, helper.PageText с cp1251 не подходит
	m := rxUpdated.FindStringSubmatch(b.String())
	if len(m) <= 5 {
		return
	}
	month, ok := months[m[2]]
	if !ok {
		return
	}
	day, _ := strconv.Atoi(m[1])
	year, _ := strconv.Atoi(m[3])
	hour, _ := strconv.Atoi(m[4])
	minute, _ := strconv.Atoi(m[5])
	r := time.Date(year, month, day, hour, minute, 0, 0, mskZone)
	return &r
}

func (t *Tracker) ParseDownloadID(b *bytes.Buffer, encoded bool) (downloadID string) {
	matches := rxDownloadID.FindStringSubmatch(b.String())
	if len(matches) <= 1 {
		return
	}
	return matches[1]
}

func (t *Tracker) ParsePoster(b *bytes.Buffer, encoded bool) (poster string) {
	matches := rxPoster.FindStringSubmatch(b.String())
	if len(matches) <= 1 {
		return
	}
	return tracker.AbsoluteURL(t.GetHost(), html.UnescapeString(matches[1]))
}

func (t *Tracker) PickTorrent(b *bytes.Buffer, encoded bool, _ tracker.Held) (pick tracker.Pick) {
	// у вложения свой номер. Без ссылки на него по номеру темы скачался бы чужой
	// файл, так что статус неизвестен
	if pick.DownloadID = t.ParseDownloadID(b, encoded); pick.DownloadID == "" {
		pick.Miss = "riperam: на странице нет ссылки на .torrent"
	}
	return
}

func (t *Tracker) TopicLink(topicID string) (link string) {
	if topicID == "" {
		return
	}
	return fmt.Sprintf("%s/viewtopic.php?t=%s", t.GetHost(), topicID)
}

func (t *Tracker) LoginAuthIncomplete(cookies []*http.Cookie) bool {
	// гостю phpBB тоже ставит phpbb_<n>_u, только с номером 1. Вошли, если номер не пустой и не 1
	for _, c := range cookies {
		if c != nil && rxUserCk.MatchString(c.Name) && c.Value != "" && c.Value != "1" {
			return false
		}
	}
	return true
}

func (t *Tracker) LoginAuthHint() string {
	return "phpBB may demand form tokens on login: paste browser cookies (phpbb_*_u, phpbb_*_sid) instead"
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
