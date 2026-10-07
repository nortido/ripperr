package tapochek

import (
	"bytes"
	"fmt"
	"net/http"
	"regexp"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/model/tracker"
)

var (
	// разметку после входа взяли из чужих описаний, без аккаунта видна только гостевая страница
	rxDownloadID = regexp.MustCompile(`download\.php\?id=(\d+)`)
	rxTrackerRow = regexp.MustCompile(`(?s)Трекер:.{0,200}?<span[^>]*>(\d{2}-\d{2}-\d{4} \d{2}:\d{2})</span>`)
	// в title у этого span лежит относительный возраст ("10 часов"), дата только в тексте
	rxRegistered = regexp.MustCompile(`Зарегистрирован(?:&nbsp;|\s)*\[\s*<span[^>]*>(\d{2}-\d{2}-\d{4} \d{2}:\d{2})</span>`)
)

var mskZone = time.FixedZone("MSK", 3*60*60) // Москва, без перехода на летнее

type Tracker struct {
	tracker.Tracker
}

func (t *Tracker) Headers() (header *http.Header) {
	header = &http.Header{}

	header.Add("Content-Type", "text/html; charset=cp1251")
	header.Add("Host", t.GetHost())

	return
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	// дата поста первого сообщения не годится: она не двигается при обновлении раздачи
	text := helper.PageText(b, encoded)
	matches := rxTrackerRow.FindStringSubmatch(text)
	if len(matches) <= 1 {
		matches = rxRegistered.FindStringSubmatch(text)
	}
	if len(matches) <= 1 {
		return
	}
	r, err := time.ParseInLocation("02-01-2006 15:04", matches[1], mskZone)
	if err != nil {
		return
	}
	return &r
}

func (t *Tracker) ParseDownloadID(b *bytes.Buffer, encoded bool) (downloadID string) {
	matches := rxDownloadID.FindStringSubmatch(helper.PageText(b, encoded))
	if len(matches) < 2 {
		return
	}
	return matches[1]
}

func (t *Tracker) PickTorrent(b *bytes.Buffer, encoded bool, _ tracker.Held) (pick tracker.Pick) {
	// нет ссылки на вложение, статус неизвестен. По номеру темы скачается другой файл
	if pick.DownloadID = t.ParseDownloadID(b, encoded); pick.DownloadID == "" {
		pick.Miss = "tapochek: на странице нет ссылки на .torrent"
	}
	return
}

func (t *Tracker) TopicLink(topicID string) (url string) {
	if topicID == "" {
		return
	}
	return fmt.Sprintf("%s/viewtopic.php?t=%s", t.GetHost(), topicID)
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
