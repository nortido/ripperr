package newstudio

import (
	"bytes"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model/tracker"
)

var (
	rxMagnet = regexp.MustCompile(`href="(magnet:\?xt=urn:btih:[^"]+)"`)
	// номера вложения, темы, сообщения и порт в комментарии (newstudio.tv:443) все
	// разные, так что качаем только по ссылке download.php со страницы
	rxDownloadID = regexp.MustCompile(`href="(?:\./|/)?download\.php\?id=(\d+)"`)
)

type Tracker struct {
	tracker.Tracker
}

func (t *Tracker) PickTorrent(b *bytes.Buffer, encoded bool, held tracker.Held) (pick tracker.Pick) {
	// пояс даты зависит от профиля, так что обновление сверяем по хешу с торрентом в клиенте
	page := b.String()
	hashes := pageHashes(page)
	if len(hashes) == 0 {
		pick.Miss = "newstudio: на странице нет хеша раздачи"
		return
	}
	if held.Hash == "" {
		pick.Miss = "newstudio: у раздачи в клиенте нет хеша"
		return
	}
	ids := pageDownloadIDs(page)
	id := ""
	if len(ids) == 1 {
		id = ids[0]
	}

	held.Hash = strings.ToLower(held.Hash)
	for _, h := range hashes {
		if h == held.Hash {
			pick.DownloadID = id
			pick.Date = held.Timestamp
			return
		}
	}
	if len(hashes) > 1 {
		pick.Miss = fmt.Sprintf("newstudio: разных хешей на странице: %d, ни один не совпал с хешем клиента", len(hashes))
		return
	}
	if len(held.Siblings) > 0 {
		pick.Miss = "newstudio: в клиенте несколько торрентов этой темы, свой на странице не найден"
		return
	}
	if id == "" {
		pick.Miss = fmt.Sprintf("newstudio: ссылок на .torrent на странице: %d вместо одной", len(ids))
		return
	}
	pick.DownloadID = id
	now := time.Now()
	pick.Date = &now
	return
}

func pageHashes(page string) (out []string) {
	seen := make(map[string]struct{})
	for _, m := range rxMagnet.FindAllStringSubmatch(page, -1) {
		hash, ok := torrentmeta.BtihFromMagnet(html.UnescapeString(m[1]))
		if !ok {
			continue
		}
		if _, dup := seen[hash]; dup {
			continue
		}
		seen[hash] = struct{}{}
		out = append(out, hash)
	}
	return
}

func pageDownloadIDs(page string) (out []string) {
	seen := make(map[string]struct{})
	for _, m := range rxDownloadID.FindAllStringSubmatch(page, -1) {
		if _, dup := seen[m[1]]; dup {
			continue
		}
		seen[m[1]] = struct{}{}
		out = append(out, m[1])
	}
	return
}

func New(tracker tracker.Tracker) *Tracker {
	return &Tracker{Tracker: tracker}
}
