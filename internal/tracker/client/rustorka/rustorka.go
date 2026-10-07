package rustorka

import (
	"bytes"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/model/tracker"
)

var (
	// разметку после входа взяли из чужих описаний и не проверяли: все страницы под
	// капчей, вход только по кукам
	rxDownloadID = regexp.MustCompile(`download\.php\?id=(\d+)`)
	rxRegistered = regexp.MustCompile(`Зарегистрирован:\s*</td>\s*<td[^>]*>\s*(\d{2})-([А-Яа-я]{3})-(\d{2}) (\d{2}):(\d{2})`)
)

var months = map[string]time.Month{
	"янв": time.January, "фев": time.February, "мар": time.March, "апр": time.April,
	"май": time.May, "июн": time.June, "июл": time.July, "авг": time.August,
	"сен": time.September, "окт": time.October, "ноя": time.November, "дек": time.December,
}

var mskZone = time.FixedZone("MSK", 3*60*60) // московское время, без летнего

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
	m := rxRegistered.FindStringSubmatch(helper.PageText(b, encoded))
	if len(m) < 6 {
		return
	}
	month, ok := months[strings.ToLower(m[2])]
	if !ok {
		return
	}
	nums := make([]int, 4)
	for i, s := range []string{m[1], m[3], m[4], m[5]} {
		nums[i], _ = strconv.Atoi(s)
	}
	r := time.Date(2000+nums[1], month, nums[0], nums[2], nums[3], 0, 0, mskZone)
	if r.Day() != nums[0] {
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
	// без ссылки на вложение статус неизвестен: по номеру темы скачался бы чужой файл
	if pick.DownloadID = t.ParseDownloadID(b, encoded); pick.DownloadID == "" {
		pick.Miss = "rustorka: на странице нет ссылки на .torrent"
	}
	return
}

func (t *Tracker) TopicLink(topicID string) (url string) {
	if topicID == "" {
		return
	}
	return fmt.Sprintf("%s/forum/viewtopic.php?t=%s", t.GetHost(), topicID)
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
