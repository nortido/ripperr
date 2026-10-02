package rutor

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"time"

	"ripperr/internal/model/tracker"
)

var (
	rxAdded = regexp.MustCompile(`Добавлен</td><td>(\d{2}-\d{2}-\d{4}) (\d{1,2}):(\d{2}:\d{2})`)
	// только из блока #download. Ниже лежат магнеты связанных раздач, жадный поиск утащил бы чужой
	rxMagnet = regexp.MustCompile(`id="download">\s*<a href="(magnet:\?[^"]+)"`)
)

var mskZone = time.FixedZone("MSK", 3*60*60) // у rutor московское время, летнего нет

type Tracker struct {
	tracker.Tracker
}

func (t *Tracker) Headers() (header *http.Header) {
	header = &http.Header{}

	header.Add("Content-Type", "text/html; charset=UTF-8")
	header.Add("Host", t.GetHost())
	header.Add("Referer", t.GetHost()+"/browse/")

	return
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	// rutor отдает UTF-8, helper.PageText с windows-1251 превратил бы кириллицу в мусор
	matches := rxAdded.FindStringSubmatch(b.String())
	if len(matches) <= 3 {
		return
	}
	hour := matches[2]
	if len(hour) == 1 {
		hour = "0" + hour
	}
	r, err := time.ParseInLocation("02-01-2006 15:04:05", matches[1]+" "+hour+":"+matches[3], mskZone)
	if err != nil {
		return
	}
	// закрепленные темы rutor стоят с датой 19-01-2038 (потолок 32-битного time_t).
	// Запишем ее в историю, и настоящее обновление уже не поймать. Сутки запаса на
	// разницу часов
	if r.After(time.Now().Add(24 * time.Hour)) {
		return
	}
	result = &r

	return
}

func (t *Tracker) TopicLink(topicID string) (url string) {
	if topicID == "" {
		return
	}
	return fmt.Sprintf("%s/torrent/%s", t.GetHost(), topicID)
}

func (t *Tracker) ParseMagnet(b *bytes.Buffer, encoded bool) (magnet string) {
	matches := rxMagnet.FindStringSubmatch(b.String())
	if len(matches) < 2 {
		return
	}
	return html.UnescapeString(matches[1])
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}

var rxPoster = regexp.MustCompile(`id="details"[\s\S]*?<img[^>]+src="([^"]+)"`)

func (t *Tracker) ParsePoster(b *bytes.Buffer, encoded bool) (poster string) {
	// выше #details только логотип и иконки шапки
	matches := rxPoster.FindStringSubmatch(b.String())
	if len(matches) <= 1 {
		return
	}
	return tracker.AbsoluteURL(t.GetHost(), html.UnescapeString(matches[1]))
}
