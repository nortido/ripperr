package selezen

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
	rxHost = regexp.MustCompile(`https?://([a-z0-9-]+\.)?selezen\.(top|club|org|net|site)/`)
	// магнет после входа взят из Prowlarr (selezen.yml), вживую не проверяли
	rxMagnet = regexp.MustCompile(`href="(magnet:\?xt=[^"]+)"`)
	rxOgImg  = regexp.MustCompile(`<meta[^>]+property="og:image"[^>]+content="([^"]+)"`)
)

type Tracker struct {
	tracker.Tracker
}

func New(t tracker.Tracker) *Tracker {
	return &Tracker{Tracker: t}
}

func (t *Tracker) Detect(link string) bool {
	return t.Tracker.Detect(link) || rxHost.MatchString(link)
}

func (t *Tracker) ParseMagnet(b *bytes.Buffer, encoded bool) (magnet string) {
	matches := rxMagnet.FindStringSubmatch(b.String())
	if len(matches) < 2 {
		return
	}
	return html.UnescapeString(matches[1])
}

func (t *Tracker) PickTorrent(b *bytes.Buffer, encoded bool, held tracker.Held) (pick tracker.Pick) {
	// что меняется в теме при перезаливке, нигде не описано, поэтому смену видим по infohash магнета
	magnets := rxMagnet.FindAllStringSubmatch(b.String(), -1)
	if len(magnets) == 0 {
		pick.Miss = "selezen: на странице нет magnet-ссылки, нужен вход"
		return
	}
	hashes := make(map[string]struct{}, len(magnets))
	for _, m := range magnets {
		if hash, ok := torrentmeta.BtihFromMagnet(html.UnescapeString(m[1])); ok {
			hashes[hash] = struct{}{}
		}
	}
	if len(hashes) == 0 {
		pick.Miss = "selezen: в magnet-ссылке нет infohash"
		return
	}
	if _, ok := hashes[strings.ToLower(held.Hash)]; ok && held.Timestamp != nil {
		pick.Date = held.Timestamp
		return
	}
	// свой магнет тут не угадать. Чужой (из коментария, соседнего релиза или от соседа
	// по теме) приняли бы за обновление и заменили им живую загрузку
	if len(hashes) > 1 {
		pick.Miss = fmt.Sprintf("selezen: разных magnet-ссылок на странице: %d, ни одна не совпала с клиентом", len(hashes))
		return
	}
	if len(held.Siblings) > 0 {
		pick.Miss = "selezen: в клиенте несколько торрентов этой темы, свой на странице не найден"
		return
	}
	cur := time.Now()
	pick.Date = &cur
	return
}

func (t *Tracker) DownloadLink(topicID string) (url string) {
	// известного адреса .torrent нет, лучше громкая ошибка, чем придуманный URL
	return ""
}

func (t *Tracker) ParsePoster(b *bytes.Buffer, encoded bool) (poster string) {
	matches := rxOgImg.FindStringSubmatch(b.String())
	if len(matches) < 2 {
		return
	}
	return tracker.AbsoluteURL(t.GetHost(), html.UnescapeString(matches[1]))
}
