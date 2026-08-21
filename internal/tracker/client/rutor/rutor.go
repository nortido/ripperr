package rutor

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"time"

	"trackerr/internal/model/tracker"
)

var (
	rxAdded = regexp.MustCompile(`Добавлен</td><td>(\d{2}-\d{2}-\d{4}) (\d{1,2}):(\d{2}:\d{2})`)
	// магнет берётся только из самого блока #download: ниже на странице лежат
	// магнеты связанных раздач, и жадный поиск утащил бы чужую раздачу
	rxMagnet = regexp.MustCompile(`id="download">\s*<a href="(magnet:\?[^"]+)"`)
)

// на страницах rutor время московское, перехода на летнее в этой зоне нет
var mskZone = time.FixedZone("MSK", 3*60*60)

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

// страницы rutor отдаются в UTF-8, поэтому helper.PageText с декодером
// windows-1251 тут не годится: кириллица в разметке превратится в мусор.
func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
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
	// закреплённые темы rutor помечены датой из будущего (19-01-2038, потолок
	// 32-битного time_t), чтобы висеть вверху списков. Такая дата всегда позже
	// времени добавления торрента, то есть раздача выглядела бы обновлённой, а
	// после записи 2038 в историю настоящее обновление уже не поймать никогда.
	// Сутки запаса на расхождение часов, дальше дата считается нераспознанной.
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

// постер лежит первой картинкой внутри таблицы описания: всё, что выше по
// странице, это логотип и иконки шапки
var rxPoster = regexp.MustCompile(`id="details"[\s\S]*?<img[^>]+src="([^"]+)"`)

func (t *Tracker) ParsePoster(b *bytes.Buffer, encoded bool) (poster string) {
	matches := rxPoster.FindStringSubmatch(b.String())
	if len(matches) <= 1 {
		return
	}
	return tracker.AbsoluteURL(t.GetHost(), html.UnescapeString(matches[1]))
}
