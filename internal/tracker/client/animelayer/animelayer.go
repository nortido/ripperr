package animelayer

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ripperr/internal/model/tracker"
)

var (
	rxTorrentURL = regexp.MustCompile(`/torrent/([0-9a-f]{24})(?:/|$)`)
	rxHexID      = regexp.MustCompile(`^[0-9a-f]{24}$`)
	rxUpdated    = regexp.MustCompile(`class="date-updated">([^<]+)<`)
	rxCreated    = regexp.MustCompile(`class="date-created">([^<]+)<`)
	rxDate       = regexp.MustCompile(`(\d{1,2}) ([а-я]+)(?: (\d{4}))? в (\d{1,2}):(\d{2})`)
	rxCover      = regexp.MustCompile(`<div class="cover">[\s\S]*?<img[^>]+src="([^"]+)"`)
)

var months = map[string]time.Month{
	"января": time.January, "февраля": time.February, "марта": time.March,
	"апреля": time.April, "мая": time.May, "июня": time.June,
	"июля": time.July, "августа": time.August, "сентября": time.September,
	"октября": time.October, "ноября": time.November, "декабря": time.December,
}

var (
	mskZone = time.FixedZone("MSK", 3*60*60)
	now     = time.Now
)

type Tracker struct {
	tracker.Tracker
}

func (t *Tracker) PickTorrent(b *bytes.Buffer, encoded bool, held tracker.Held) (pick tracker.Pick) {
	// id раздачи это 24 hex из адреса. TopicID из первой группы цифр (вроде "6")
	// для скачивания не годится
	matches := rxTorrentURL.FindStringSubmatch(held.URL)
	if len(matches) <= 1 {
		pick.Miss = "animelayer: в адресе нет id раздачи"
		return
	}
	pick.DownloadID = matches[1]
	return
}

func (t *Tracker) DownloadLink(topicID string) (url string) {
	if !rxHexID.MatchString(topicID) {
		return
	}
	// гостя со скачивания редиректит на раздачу, адрес взяли у TorrentMonitor и Jackett.
	// Вживую не проверяли
	return fmt.Sprintf("%s%s/download/", t.DownloadURL, topicID)
}

func (t *Tracker) TopicLink(topicID string) (url string) {
	if topicID == "" {
		return
	}
	return fmt.Sprintf("%s/torrent/%s/", t.GetHost(), topicID)
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	// страницы в UTF-8, через helper.PageText с cp1251 кириллица побилась бы
	text := b.String()
	matches := rxUpdated.FindStringSubmatch(text)
	// раздачу ни разу не обновляли, отметки нет. Берем дату создания
	if len(matches) <= 1 {
		matches = rxCreated.FindStringSubmatch(text)
	}
	if len(matches) <= 1 {
		return
	}
	label := strings.ReplaceAll(html.UnescapeString(matches[1]), " ", " ")
	m := rxDate.FindStringSubmatch(label)
	if len(m) <= 5 {
		return
	}
	month, ok := months[m[2]]
	if !ok {
		return
	}
	day, _ := strconv.Atoi(m[1])
	hour, _ := strconv.Atoi(m[4])
	minute, _ := strconv.Atoi(m[5])
	current := now().In(mskZone)
	year := current.Year()
	if m[3] != "" {
		year, _ = strconv.Atoi(m[3])
	}
	r := time.Date(year, month, day, hour, minute, 0, 0, mskZone)
	// год пишут только для прошлых лет. Без этой проверки дата из конца декабря,
	// прочитаная в январе, уехала бы почти на год вперед
	if m[3] == "" && r.After(current.Add(24*time.Hour)) {
		r = r.AddDate(-1, 0, 0)
	}
	return &r
}

func (t *Tracker) ParsePoster(b *bytes.Buffer, encoded bool) (poster string) {
	matches := rxCover.FindStringSubmatch(b.String())
	if len(matches) <= 1 {
		return
	}
	return tracker.AbsoluteURL(t.GetHost(), html.UnescapeString(matches[1]))
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
