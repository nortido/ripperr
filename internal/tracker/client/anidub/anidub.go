package anidub

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"ripperr/internal/model/tracker"
)

var (
	// разметку взяли из чужих описаний и гостевой страницы, вид после входа вживую не видели
	rxBlock = regexp.MustCompile(`id=['"]torrent_(\d+)_info['"]`)
	rxTitle = regexp.MustCompile(`title="([^"]+\.torrent)"`)
	rxSize  = regexp.MustCompile(`Размер:[^<]*<span class="red">([^<]+)</span>`)
	rxDate  = regexp.MustCompile(`<li><b>Дата:</b>\s*([^<]+)</li>`)
	rxRel   = regexp.MustCompile(`(?i)^(сегодня|вчера),\s*(\d{1,2}):(\d{2})$`)
	rxPost  = regexp.MustCompile(`<span class="poster"><img src="([^"]+)"`)
)

var mskZone = time.FixedZone("MSK", 3*60*60) // на страницах Москва, летнего времени нет

var now = time.Now

type Tracker struct {
	tracker.Tracker
}

func New(t tracker.Tracker) *Tracker {
	return &Tracker{Tracker: t}
}

type block struct {
	id, title, size string
}

func (b block) downloadID() string {
	return b.id + "|" + b.title + "|" + b.size
}

func blocks(page string) []block {
	idx := rxBlock.FindAllStringSubmatchIndex(page, -1)
	out := make([]block, 0, len(idx))
	for i, m := range idx {
		end := len(page)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		part := page[m[1]:end]
		bl := block{id: page[m[2]:m[3]]}
		if s := rxTitle.FindStringSubmatch(part); s != nil {
			bl.title = html.UnescapeString(s[1])
		}
		if s := rxSize.FindStringSubmatch(part); s != nil {
			bl.size = strings.TrimSpace(s[1])
		}
		out = append(out, bl)
	}
	return out
}

func storedParts(topicID string) (id, title string) {
	// имя файла между первым и последним "|": черта бывает и в самом названии
	first := strings.Index(topicID, "|")
	if first < 0 {
		return topicID, ""
	}
	id = topicID[:first]
	rest := topicID[first+1:]
	if last := strings.LastIndex(rest, "|"); last >= 0 {
		return id, rest[:last]
	}
	return id, rest
}

func only(list []block, match func(block) bool) (block, bool) {
	var found block
	n := 0
	for _, bl := range list {
		if match(bl) {
			found = bl
			n++
		}
	}
	return found, n == 1
}

func (t *Tracker) PickTorrent(b *bytes.Buffer, encoded bool, held tracker.Held) (pick tracker.Pick) {
	// ищем раздачу из клиента сначала по имени, потом по номеру из истории. Если
	// вариантов несколько, первый блок не берем, иначе в клиент уйдет не то качество
	list := blocks(b.String())
	if held.Name != "" {
		if bl, ok := only(list, func(x block) bool { return strings.TrimSuffix(x.title, ".torrent") == held.Name }); ok {
			pick.DownloadID = bl.downloadID()
			if len(held.Siblings) > 0 {
				// номер в истории мог записать сосед по теме, так что смену видим по дате темы
				pick.Date = t.ParseDate(b, encoded)
			}
			return
		}
	}
	if len(held.Siblings) > 0 {
		pick.Miss = fmt.Sprintf("anidub: в клиенте несколько торрентов этой темы, %s не найден среди раздач (%d)", held.Name, len(list))
		return
	}
	if held.Stored {
		storedID, storedTitle := storedParts(held.TopicID)
		// номеру из истории верим, только если его записал этот же торрент.
		// Соседа может не быть в клиенте
		if strings.TrimSuffix(storedTitle, ".torrent") != held.Name {
			pick.Miss = fmt.Sprintf("anidub: запись в истории принадлежит другому торренту темы, %s не найден среди раздач (%d)", held.Name, len(list))
			return
		}
		if bl, ok := only(list, func(x block) bool { return x.id == storedID }); ok && storedID != "" {
			pick.DownloadID = bl.downloadID()
			return
		}
	}
	if len(list) == 1 {
		pick.DownloadID = list[0].downloadID()
		return
	}
	pick.Miss = fmt.Sprintf("anidub: раздач на странице: %d, ни одна не совпала с %s", len(list), held.Name)
	return
}

func (t *Tracker) DownloadLink(topicID string) (url string) {
	id, _, _ := strings.Cut(topicID, "|")
	return t.Tracker.DownloadLink(id)
}

func (t *Tracker) ParseDate(b *bytes.Buffer, encoded bool) (result *time.Time) {
	m := rxDate.FindStringSubmatch(b.String())
	if m == nil {
		return
	}
	text := strings.TrimSpace(html.UnescapeString(m[1]))
	if r, err := time.ParseInLocation("2-01-2006, 15:04", text, mskZone); err == nil {
		return &r
	}
	rel := rxRel.FindStringSubmatch(text)
	if rel == nil {
		return
	}
	var hour, minute int
	if _, err := fmt.Sscanf(rel[2]+" "+rel[3], "%d %d", &hour, &minute); err != nil || hour > 23 || minute > 59 {
		return
	}
	day := now().In(mskZone)
	if strings.EqualFold(rel[1], "вчера") {
		day = day.AddDate(0, 0, -1)
	}
	r := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, 0, 0, mskZone)
	return &r
}

func (t *Tracker) ParsePoster(b *bytes.Buffer, encoded bool) (poster string) {
	m := rxPost.FindStringSubmatch(b.String())
	if m == nil {
		return
	}
	return tracker.AbsoluteURL(t.GetHost(), html.UnescapeString(m[1]))
}
