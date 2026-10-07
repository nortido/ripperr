package aniliberty

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"ripperr/internal/model/tracker"
)

const releaseJSON = `{"id":10364,"alias":"cyberpunk-edgerunners-2","updated_at":"2026-10-22T05:46:04+00:00","poster":{"src":"\/storage\/releases\/posters\/10364\/ZN0IPdnK9cDYKIEc8G4uEaOR6UruTh34.jpg"},"torrents":[{"id":40479,"hash":"79f299dd6623be7aef40fd0bd825ac4a534355de","quality":{"value":"1080p"},"codec":{"value":"x264\/AVC"},"type":{"value":"WEB-DL"},"updated_at":"2026-10-22T01:52:16+00:00"},{"id":40484,"hash":"a5161b462482f3848fd5889733026ee11d4bc680","quality":{"value":"1080p"},"codec":{"value":"x265\/HEVC"},"type":{"value":"WEB-DL"},"updated_at":"2026-10-22T05:46:04+00:00"}]}`

const apiPrefix = "https://aniliberty.top/api/v1/anime/releases/"
const apiInclude = "?include=id,alias,updated_at,poster.src,torrents.id,torrents.hash,torrents.quality.value,torrents.codec.value,torrents.type.value,torrents.updated_at"

func newTracker() *Tracker {
	return New(tracker.Tracker{
		Domain:      "aniliberty.top",
		DownloadURL: "https://aniliberty.top/api/v1/anime/torrents/",
	})
}

func pick(t *testing.T, body string, held tracker.Held) tracker.Pick {
	t.Helper()
	return newTracker().PickTorrent(bytes.NewBufferString(body), false, held)
}

func TestPageLinkRelease(t *testing.T) {
	tr := newTracker()
	want := apiPrefix + "cyberpunk-edgerunners-2" + apiInclude
	for _, in := range []string{
		"https://aniliberty.top/anime/releases/release/cyberpunk-edgerunners-2",
		"https://aniliberty.top/anime/releases/release/cyberpunk-edgerunners-2/episodes",
		"https://anilibria.tv/release/cyberpunk-edgerunners-2.html",
	} {
		if got := tr.PageLink(in); got != want {
			t.Errorf("PageLink(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{
		apiPrefix + "10364",
		"https://aniliberty.top/api/v1/anime/torrents/40484/file",
	} {
		if got := tr.PageLink(in); got != in {
			t.Errorf("PageLink(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestDetectLegacy(t *testing.T) {
	tr := newTracker()
	for _, in := range []string{
		"https://aniliberty.top/anime/releases/release/x",
		"https://anilibria.tv/release/x.html",
		"https://anilibria.top/anime/releases/release/x",
		"https://www.anilibria.wtf/release/x.html",
	} {
		if !tr.Detect(in) {
			t.Errorf("Detect(%q) = false", in)
		}
	}
	if tr.Detect("https://example.com/release/x.html") {
		t.Error("Detect matched a foreign host")
	}
}

func TestPickByHash(t *testing.T) {
	got := pick(t, releaseJSON, tracker.Held{Hash: "A5161B462482F3848FD5889733026EE11D4BC680"})
	want := "40484|a5161b462482f3848fd5889733026ee11d4bc680|1080p,x265/HEVC,WEB-DL"
	if got.DownloadID != want {
		t.Fatalf("DownloadID = %q (miss %q), want %q", got.DownloadID, got.Miss, want)
	}
	date := time.Date(2026, time.October, 22, 5, 46, 4, 0, time.UTC)
	if got.Date == nil || !got.Date.Equal(date) {
		t.Errorf("Date = %v, want %v", got.Date, date)
	}
}

func TestPickByStoredID(t *testing.T) {
	held := tracker.Held{Stored: true, TopicID: "40479|zzz|1080p,x264/AVC,WEB-DL", Hash: "zzz"}
	got := pick(t, releaseJSON, held)
	if !strings.HasPrefix(got.DownloadID, "40479|79f299dd") {
		t.Errorf("DownloadID = %q (miss %q), want id 40479", got.DownloadID, got.Miss)
	}
}

func TestPickStoredKindAfterReplace(t *testing.T) {
	body := strings.Replace(releaseJSON, `"id":40484`, `"id":40500`, 1)
	held := tracker.Held{Stored: true, TopicID: "40484|old|1080p,x265/HEVC,WEB-DL", Hash: "old"}
	got := pick(t, body, held)
	want := "40500|a5161b462482f3848fd5889733026ee11d4bc680|1080p,x265/HEVC,WEB-DL"
	if got.DownloadID != want {
		t.Errorf("DownloadID = %q (miss %q), want %q", got.DownloadID, got.Miss, want)
	}
}

func TestPickStoredKindNewest(t *testing.T) {
	body := `{"torrents":[` +
		`{"id":1,"hash":"aa","quality":{"value":"720p"},"codec":{"value":"x264"},"type":{"value":"WEB-DL"},"updated_at":"2026-10-01T00:00:00+00:00"},` +
		`{"id":2,"hash":"bb","quality":{"value":"720p"},"codec":{"value":"x264"},"type":{"value":"WEB-DL"},"updated_at":"2026-10-03T00:00:00+00:00"},` +
		`{"id":3,"hash":"cc","quality":{"value":"720p"},"codec":{"value":"x264"},"type":{"value":"WEB-DL"},"updated_at":"2026-10-02T00:00:00+00:00"}]}`
	got := pick(t, body, tracker.Held{Stored: true, TopicID: "9|old|720p,x264,WEB-DL", Hash: "old"})
	if !strings.HasPrefix(got.DownloadID, "2|bb|") {
		t.Errorf("DownloadID = %q, want the newest of the kind (id 2)", got.DownloadID)
	}
}

func TestPickAmbiguousMiss(t *testing.T) {
	got := pick(t, releaseJSON, tracker.Held{})
	if got.DownloadID != "" || got.Miss != "aniliberty: раздач в релизе: 2, ни одна не совпала" {
		t.Errorf("got %+v, want miss for 2 torrents", got)
	}
}

func TestPickOtherKindMiss(t *testing.T) {
	body := `{"torrents":[{"id":1,"hash":"aa","quality":{"value":"480p"},"codec":{"value":"x264"},"type":{"value":"WEB-DL"},"updated_at":"2026-10-01T00:00:00+00:00"}]}`
	got := pick(t, body, tracker.Held{Stored: true, TopicID: "40484|old|1080p,x265/HEVC,WEB-DL"})
	if got.DownloadID != "" || got.Miss == "" {
		t.Errorf("got %+v, want miss: single torrent of another kind", got)
	}
}

func TestPickSingleFirstCheck(t *testing.T) {
	body := `{"torrents":[{"id":1,"hash":"aa","quality":{"value":"480p"},"codec":{"value":"x264"},"type":{"value":"WEB-DL"},"updated_at":"2026-10-01T00:00:00+00:00"}]}`
	got := pick(t, body, tracker.Held{})
	if got.DownloadID != "1|aa|480p,x264,WEB-DL" {
		t.Errorf("got %+v, want the only torrent", got)
	}
}

func TestBadJSONMiss(t *testing.T) {
	got := pick(t, "<html>not json</html>", tracker.Held{})
	if got.Miss != "aniliberty: ответ API не разобран" {
		t.Errorf("Miss = %q", got.Miss)
	}
}

func TestDownloadLink(t *testing.T) {
	got := newTracker().DownloadLink("40484|h|k")
	want := "https://aniliberty.top/api/v1/anime/torrents/40484/file"
	if got != want {
		t.Errorf("DownloadLink = %q, want %q", got, want)
	}
}

func TestParsePoster(t *testing.T) {
	got := newTracker().ParsePoster(bytes.NewBufferString(releaseJSON), false)
	want := "https://aniliberty.top/storage/releases/posters/10364/ZN0IPdnK9cDYKIEc8G4uEaOR6UruTh34.jpg"
	if got != want {
		t.Errorf("ParsePoster = %q, want %q", got, want)
	}
	if p := newTracker().ParsePoster(bytes.NewBufferString("oops"), false); p != "" {
		t.Errorf("ParsePoster on bad JSON = %q", p)
	}
}

const adoptName = "Cyberpunk Edgerunners 2 - AniLiberty [WEB-DL 1080p HEVC]"
const adoptHash = "a5161b462482f3848fd5889733026ee11d4bc680"

func TestAdoptLookup(t *testing.T) {
	tr := newTracker()
	want := "https://aniliberty.top/api/v1/anime/torrents/" + adoptHash + "?include=id,hash,release.id,release.alias"
	for _, name := range []string{adoptName, "cyberpunk - ANILIBERTY"} {
		if got := tr.AdoptLookup(name, strings.ToUpper(adoptHash)); got != want {
			t.Errorf("AdoptLookup(%q) = %q, want %q", name, got, want)
		}
	}
	for _, c := range []struct{ name, hash string }{
		{"Cyberpunk Edgerunners 2 [WEB-DL 1080p HEVC]", adoptHash},
		{adoptName, "a5161b46"},
		{adoptName, strings.Repeat("z", 40)},
		{adoptName, ""},
	} {
		if got := tr.AdoptLookup(c.name, c.hash); got != "" {
			t.Errorf("AdoptLookup(%q, %q) = %q, want empty", c.name, c.hash, got)
		}
	}
}

func TestAdoptURL(t *testing.T) {
	tr := newTracker()
	live := `{"id":40484,"hash":"a5161b462482f3848fd5889733026ee11d4bc680","release":{"id":10364,"alias":"cyberpunk-edgerunners-2"}}`
	want := "https://aniliberty.top/anime/releases/release/cyberpunk-edgerunners-2"
	if got := tr.AdoptURL(bytes.NewBufferString(live)); got != want {
		t.Errorf("AdoptURL = %q, want %q", got, want)
	}
	for _, bad := range []string{"<html>", `{"release":{"id":1}}`, `{"release":{"alias":"A/../b"}}`} {
		if got := tr.AdoptURL(bytes.NewBufferString(bad)); got != "" {
			t.Errorf("AdoptURL(%q) = %q, want empty", bad, got)
		}
	}
}

const (
	avcHash  = "79f299dd6623be7aef40fd0bd825ac4a534355de"
	hevcHash = "a5161b462482f3848fd5889733026ee11d4bc680"
	avcName  = "Cyberpunk- Edgerunners 2 - AniLiberty.TOP [WEB-DL 1080p][AVC][1]"
	hevcName = "Cyberpunk- Edgerunners 2 - AniLiberty.TOP [WEB-DL 1080p][HEVC][1]"
	avcID    = "40479|" + avcHash + "|1080p,x264/AVC,WEB-DL"
	hevcID   = "40484|" + hevcHash + "|1080p,x265/HEVC,WEB-DL"
)

func TestPickHeldHashIsUnchangedWhateverStored(t *testing.T) {
	stamp := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	held := tracker.Held{Stored: true, TopicID: hevcID, Hash: avcHash, Name: avcName, Timestamp: &stamp, Siblings: []string{hevcHash}}
	got := pick(t, releaseJSON, held)
	if got.DownloadID != avcID || got.Date == nil || !got.Date.Equal(stamp) {
		t.Errorf("got %+v, want own torrent dated %v", got, stamp)
	}
}

func TestPickSiblingFollowsOwnKind(t *testing.T) {
	stamp := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)
	body := strings.NewReplacer(avcHash, "1111111111111111111111111111111111111111", hevcHash, "2222222222222222222222222222222222222222").Replace(releaseJSON)
	held := tracker.Held{Stored: true, TopicID: hevcID, Hash: avcHash, Name: avcName, Timestamp: &stamp, Siblings: []string{hevcHash}}
	before := time.Now()
	got := pick(t, body, held)
	if !strings.HasPrefix(got.DownloadID, "40479|1111") || got.Date == nil || got.Date.Before(before) {
		t.Errorf("got %+v, want the new AVC torrent dated now", got)
	}
}

func TestPickSiblingAmbiguousMiss(t *testing.T) {
	body := strings.NewReplacer(avcHash, "1111111111111111111111111111111111111111", hevcHash, "2222222222222222222222222222222222222222").Replace(releaseJSON)
	held := tracker.Held{Stored: true, TopicID: hevcID, Hash: avcHash, Name: "Cyberpunk [WEB-DL 1080p]", Siblings: []string{hevcHash}}
	got := pick(t, body, held)
	if got.Miss == "" || got.DownloadID != "" {
		t.Errorf("got %+v, want Miss: both torrents fit the name", got)
	}
}

func TestPickSiblingNeverTakesSiblingTorrent(t *testing.T) {
	body := strings.Replace(releaseJSON, avcHash, "1111111111111111111111111111111111111111", 1)
	held := tracker.Held{Stored: true, TopicID: avcID, Hash: "3333333333333333333333333333333333333333", Name: hevcName, Siblings: []string{hevcHash}}
	got := pick(t, body, held)
	if got.Miss == "" || got.DownloadID != "" {
		t.Errorf("got %+v, want Miss: the only HEVC torrent is the sibling's", got)
	}
}

func TestPickStoredIDNewHashIsUpdate(t *testing.T) {
	held := tracker.Held{Stored: true, TopicID: "40479|zzz|1080p,x264/AVC,WEB-DL", Hash: "zzz"}
	before := time.Now()
	got := pick(t, releaseJSON, held)
	if got.DownloadID != avcID || got.Date == nil || got.Date.Before(before) {
		t.Errorf("got %+v, want update dated now", got)
	}
}

func TestDetectLegacyHostOnly(t *testing.T) {
	tr := newTracker()
	for _, in := range []string{
		"https://rutracker.org/forum/viewtopic.php?t=1&from=anilibria.tv",
		"https://example.com/anilibria.top/release/x.html",
		"https://notanilibria.tv/release/x.html",
	} {
		if tr.Detect(in) {
			t.Errorf("Detect(%q) = true, want false", in)
		}
	}
	if !tr.Detect("http://anilibria.tv:8080/release/x.html") {
		t.Error("Detect missed a legacy host with port")
	}
}

func TestPickStoredRowOfOtherTorrentNotFollowed(t *testing.T) {
	body := strings.NewReplacer(avcHash, "1111111111111111111111111111111111111111", hevcHash, "2222222222222222222222222222222222222222").Replace(releaseJSON)
	held := tracker.Held{Stored: true, TopicID: hevcID, Hash: avcHash, Name: avcName}
	got := pick(t, body, held)
	if !strings.HasPrefix(got.DownloadID, "40479|1111") {
		t.Errorf("got %+v, want the new AVC torrent: the row was written by the HEVC sibling", got)
	}
	held.Name = "renamed by user"
	if got = pick(t, body, held); got.Miss == "" || got.DownloadID != "" {
		t.Errorf("got %+v, want Miss: foreign row and no kind in the name", got)
	}
}

func TestPickOwnStoredRowFollowsID(t *testing.T) {
	body := strings.Replace(releaseJSON, avcHash, "1111111111111111111111111111111111111111", 1)
	held := tracker.Held{Stored: true, TopicID: avcID, Hash: strings.ToUpper(avcHash), Name: "renamed by user"}
	if got := pick(t, body, held); !strings.HasPrefix(got.DownloadID, "40479|1111") {
		t.Errorf("got %+v, want id 40479 from the own row", got)
	}
}
