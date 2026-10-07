package aniliberty

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ripperr/internal/model/tracker"
)

const adoptInclude = "?include=id,hash,release.id,release.alias"

const releaseInclude = "?include=id,alias,updated_at,poster.src,torrents.id,torrents.hash,torrents.quality.value,torrents.codec.value,torrents.type.value,torrents.updated_at"

var (
	rxAlias       = regexp.MustCompile(`/anime/releases/release/([a-z0-9-]+)`)
	rxLegacyAlias = regexp.MustCompile(`/release/([a-z0-9-]+)\.html`)
	rxLegacyHost  = regexp.MustCompile(`(?i)^https?://(?:[a-z0-9-]+\.)*anilibria\.(?:top|tv|wtf)(?::\d+)?(?:[/?#]|$)`)
	rxHash        = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	rxSafeAlias   = regexp.MustCompile(`^[a-z0-9-]+$`)
)

type value struct {
	Value string `json:"value"`
}

type torrent struct {
	ID        int    `json:"id"`
	Hash      string `json:"hash"`
	Quality   value  `json:"quality"`
	Codec     value  `json:"codec"`
	Type      value  `json:"type"`
	UpdatedAt string `json:"updated_at"`
}

type release struct {
	Poster struct {
		Src string `json:"src"`
	} `json:"poster"`
	Torrents []torrent `json:"torrents"`
}

func (x torrent) kind() string {
	return x.Quality.Value + "," + x.Codec.Value + "," + x.Type.Value
}

func (x torrent) downloadID() string {
	return fmt.Sprintf("%d|%s|%s", x.ID, x.Hash, x.kind())
}

func (x torrent) updated() *time.Time {
	d, err := time.Parse(time.RFC3339, x.UpdatedAt)
	if err != nil {
		return nil
	}
	return &d
}

type Tracker struct {
	tracker.Tracker
}

func New(t tracker.Tracker) *Tracker {
	return &Tracker{Tracker: t}
}

func (t *Tracker) Detect(link string) bool {
	return t.Tracker.Detect(link) || rxLegacyHost.MatchString(link)
}

func (t *Tracker) PageLink(link string) string {
	m := rxAlias.FindStringSubmatch(link)
	if m == nil {
		m = rxLegacyAlias.FindStringSubmatch(link)
	}
	if m == nil {
		return link
	}
	return t.GetHost() + "/api/v1/anime/releases/" + m[1] + releaseInclude
}

func (t *Tracker) DownloadLink(topicID string) string {
	// DownloadID составной (id|hash|вид), для скачивания нужна часть до "|"
	id, _, _ := strings.Cut(topicID, "|")
	return t.DownloadURL + id + "/file"
}

func (t *Tracker) PickTorrent(b *bytes.Buffer, encoded bool, held tracker.Held) tracker.Pick {
	var r release
	if err := json.Unmarshal(b.Bytes(), &r); err != nil {
		return tracker.Pick{Miss: "aniliberty: ответ API не разобран"}
	}

	hash := strings.ToLower(held.Hash)
	for _, x := range r.Torrents {
		if hash == "" || strings.ToLower(x.Hash) != hash {
			continue
		}
		// торрент из клиента все еще в релизе, значит не менялся. Номер в истории тут
		// не смотрим, строку делят все торренты релиза
		date := held.Timestamp
		if date == nil {
			date = x.updated()
		}
		return tracker.Pick{DownloadID: x.downloadID(), Date: date}
	}

	// номеру и виду из истории верим, только если их записал этот же торрент.
	// Соседа по релизу может не быть в клиенте
	var picked *torrent
	if held.Stored && ownRow(held) || !held.Stored && len(held.Siblings) == 0 {
		picked = followStored(r.Torrents, held)
	} else {
		picked = ownKind(r.Torrents, held)
	}
	if picked == nil {
		return tracker.Pick{Miss: fmt.Sprintf("aniliberty: раздач в релизе: %d, ни одна не совпала", len(r.Torrents))}
	}
	// раннер верит дате трекера больше, чем смене номера, поэтому смену отмечаем здесь
	if held.Stored && picked.downloadID() != held.TopicID {
		now := time.Now()
		return tracker.Pick{DownloadID: picked.downloadID(), Date: &now}
	}
	return tracker.Pick{DownloadID: picked.downloadID(), Date: picked.updated()}
}

func followStored(list []torrent, held tracker.Held) (picked *torrent) {
	var storedID, storedKind string
	if held.Stored {
		parts := strings.SplitN(held.TopicID, "|", 3)
		storedID = parts[0]
		if len(parts) == 3 {
			storedKind = parts[2]
		}
	}
	if storedID != "" {
		for i, x := range list {
			if strconv.Itoa(x.ID) == storedID {
				return &list[i]
			}
		}
	}
	if storedKind != "" {
		for i, x := range list {
			if x.kind() != storedKind {
				continue
			}
			if picked == nil || x.updated() != nil && (picked.updated() == nil || x.updated().After(*picked.updated())) {
				picked = &list[i]
			}
		}
	}
	// одну раздачу берем, только пока вид неизвестен. Потом другой вид значит подмену качества или кодека
	if picked == nil && len(list) == 1 && storedKind == "" {
		picked = &list[0]
	}
	return
}

func ownRow(held tracker.Held) bool {
	parts := strings.SplitN(held.TopicID, "|", 3)
	return len(parts) == 3 && held.Hash != "" && strings.EqualFold(parts[1], held.Hash)
}

func ownKind(list []torrent, held tracker.Held) *torrent {
	// номер из истории мог записать сосед по релизу, поэтому свой ищем по качеству,
	// кодеку и типу в имени. Раздачи, занятые соседями, пропускаем
	name := strings.ToLower(held.Name)
	taken := make(map[string]struct{}, len(held.Siblings))
	for _, s := range held.Siblings {
		taken[strings.ToLower(s)] = struct{}{}
	}
	var found *torrent
	n := 0
	for i, x := range list {
		if _, ok := taken[strings.ToLower(x.Hash)]; ok || !x.fits(name) {
			continue
		}
		found = &list[i]
		n++
	}
	if n != 1 {
		return nil
	}
	return found
}

func (x torrent) fits(name string) bool {
	// в имени кодек стоит меткой ("AVC"), а API отдает "x264/AVC"
	codec := x.Codec.Value
	if i := strings.LastIndex(codec, "/"); i >= 0 {
		codec = codec[i+1:]
	}
	for _, v := range []string{x.Quality.Value, codec, x.Type.Value} {
		if v == "" || !strings.Contains(name, strings.ToLower(v)) {
			return false
		}
	}
	return true
}

func (t *Tracker) ParsePoster(b *bytes.Buffer, encoded bool) string {
	var r release
	if err := json.Unmarshal(b.Bytes(), &r); err != nil {
		return ""
	}
	return tracker.AbsoluteURL(t.GetHost(), r.Poster.Src)
}

func (t *Tracker) AdoptLookup(name, hash string) string {
	// у торрента, добавленного руками, нет комментария, релиз ищем в API по инфохешу.
	// Смысл есть только для торрентов самого AniLiberty
	if !rxHash.MatchString(hash) || !strings.Contains(strings.ToLower(name), "aniliberty") {
		return ""
	}
	return t.GetHost() + "/api/v1/anime/torrents/" + strings.ToLower(hash) + adoptInclude
}

func (t *Tracker) AdoptURL(b *bytes.Buffer) string {
	var r struct {
		Release struct {
			Alias string `json:"alias"`
		} `json:"release"`
	}
	if err := json.Unmarshal(b.Bytes(), &r); err != nil || !rxSafeAlias.MatchString(r.Release.Alias) {
		return ""
	}
	return t.GetHost() + "/anime/releases/release/" + r.Release.Alias
}
