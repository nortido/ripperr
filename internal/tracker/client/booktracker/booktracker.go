package booktracker

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"time"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model/tracker"
)

var (
	// магнет и ссылку в блоке вложения после входа взяли из чужих описаний, вживую
	// не проверяли. Гостю магнет виден
	rxMagnet     = regexp.MustCompile(`<a href="(magnet:\?[^"]+)" class="magnet-link"`)
	rxTorHash    = regexp.MustCompile(`<span id="tor-hash"[^>]*>\s*([0-9A-Fa-f]{40})\s*</span>`)
	rxDownloadID = regexp.MustCompile(`download\.php\?id=(\d+)`)
	rxAttachOpen = regexp.MustCompile(`<table[^>]*class="attach`)
	rxTable      = regexp.MustCompile(`<table\b|</table>`)
)

type Tracker struct {
	tracker.Tracker
}

type attach struct {
	hash       string
	downloadID string
}

func (t *Tracker) Headers() (header *http.Header) {
	header = &http.Header{}

	header.Add("Content-Type", "text/html; charset=UTF-8")
	header.Add("Host", t.GetHost())

	return
}

func (t *Tracker) TopicLink(topicID string) (url string) {
	if topicID == "" {
		return
	}
	return fmt.Sprintf("%s/viewtopic.php?t=%s", t.GetHost(), topicID)
}

func (t *Tracker) ParseMagnet(b *bytes.Buffer, encoded bool) (magnet string) {
	matches := rxMagnet.FindStringSubmatch(b.String())
	if len(matches) < 2 {
		return
	}
	return html.UnescapeString(matches[1])
}

func (t *Tracker) PickTorrent(b *bytes.Buffer, encoded bool, held tracker.Held) (pick tracker.Pick) {
	// формат и пояс даты зависят от профиля, поэтому обновление ловим по хешу из клиента
	page := b.String()
	blocks := attachBlocks(page)
	guest := len(blocks) == 0
	if guest {
		// у гостя блока вложения может не быть, а магнет на странице есть
		blocks = []string{page}
	}
	var found []attach
	for _, block := range blocks {
		if a := parseAttach(block); a.hash != "" {
			found = append(found, a)
		}
	}
	if len(found) == 0 {
		pick.Miss = "booktracker: на странице нет хеша раздачи"
		return
	}
	if held.Hash == "" {
		pick.Miss = "booktracker: у раздачи в клиенте нет хеша"
		return
	}
	held.Hash = strings.ToLower(held.Hash)

	for _, a := range found {
		if a.hash == held.Hash {
			pick.DownloadID = a.downloadID
			pick.Date = held.Timestamp
			return
		}
	}
	onPage := pageHashes(page)
	if _, ok := onPage[held.Hash]; ok && guest {
		pick.Date = held.Timestamp
		return
	}
	if len(blocks) > 1 {
		pick.Miss = fmt.Sprintf("booktracker: раздач на странице: %d, ни одна не совпала с хешем клиента", len(blocks))
		return
	}
	// первый магнет страницы мог прийти из комментария или соседнего релиза
	if len(onPage) > 1 {
		pick.Miss = fmt.Sprintf("booktracker: разных хешей на странице: %d, ни один не совпал с хешем клиента", len(onPage))
		return
	}
	if len(held.Siblings) > 0 {
		pick.Miss = "booktracker: в клиенте несколько торрентов этой темы, свой на странице не найден"
		return
	}
	// номер вложения может не совпадать с номером темы, скачивание по теме взяло бы чужой файл
	if found[0].downloadID == "" {
		pick.Miss = "booktracker: на странице нет ссылки на .torrent"
		return
	}
	pick.DownloadID = found[0].downloadID
	now := time.Now()
	pick.Date = &now
	return
}

func pageHashes(page string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, m := range rxMagnet.FindAllStringSubmatch(page, -1) {
		if hash, ok := torrentmeta.BtihFromMagnet(html.UnescapeString(m[1])); ok {
			out[hash] = struct{}{}
		}
	}
	for _, m := range rxTorHash.FindAllStringSubmatch(page, -1) {
		out[strings.ToLower(m[1])] = struct{}{}
	}
	return out
}

func parseAttach(block string) (a attach) {
	if m := rxMagnet.FindStringSubmatch(block); len(m) > 1 {
		a.hash, _ = torrentmeta.BtihFromMagnet(html.UnescapeString(m[1]))
	}
	if a.hash == "" {
		if m := rxTorHash.FindStringSubmatch(block); len(m) > 1 {
			a.hash = strings.ToLower(m[1])
		}
	}
	if m := rxDownloadID.FindStringSubmatch(block); len(m) > 1 {
		a.downloadID = m[1]
	}
	return
}

func attachBlocks(page string) (blocks []string) {
	// таблицы бывают вложенные, поэтому считаем глубину до своего </table>
	pos := 0
	for {
		loc := rxAttachOpen.FindStringIndex(page[pos:])
		if loc == nil {
			return
		}
		start := pos + loc[0]
		depth := 0
		end := len(page)
		for _, tag := range rxTable.FindAllStringIndex(page[start:], -1) {
			if strings.HasPrefix(page[start+tag[0]:], "</") {
				depth--
			} else {
				depth++
			}
			if depth == 0 {
				end = start + tag[1]
				break
			}
		}
		blocks = append(blocks, page[start:end])
		pos = end
	}
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{
		Tracker: tracker,
	}
}
