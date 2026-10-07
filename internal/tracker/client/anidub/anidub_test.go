package anidub

import (
	"bytes"
	"testing"
	"time"

	"ripperr/internal/model/tracker"
)

const pokemonPage = `<ul class="story_inf"><li><b>Добавил:</b> x</li><li><b>Дата:</b> 18-10-2026, 11:31</li></ul>
<div id="tv1080">
<div id='torrent_45147_info'><a href="/engine/download.php?id=45147"><span class="">Скачать торрент!</span></a>
Размер: <span class="red">41.23 GB</span> <span class="red" title="[AniDub]_Pokemon_Horizons_100+.torrent">x</span></div>
<div id='torrent_41936_info'><a href="/engine/download.php?id=41936"><span class="">Скачать торрент!</span></a>
Размер: <span class="red">66.16 GB</span> <span class="red" title="[AniDub]_Pokemon_Horizons.torrent">x</span></div>
</div>`

const singlePage = `<div id="tv1080"><div id='torrent_45155_info'>Размер: <span class="red">13.74 GB</span> <span class="red" title="[AniDub]_Ace.of.Diamond.Act.II.s02.torrent">x</span></div></div>`

func buf(s string) *bytes.Buffer { return bytes.NewBufferString(s) }

func newTracker() *Tracker {
	return New(tracker.Tracker{Domain: "tr.anidub.com", DownloadURL: "https://tr.anidub.com/engine/download.php?id="})
}

func TestPickByName(t *testing.T) {
	got := newTracker().PickTorrent(buf(pokemonPage), true, tracker.Held{Name: "[AniDub]_Pokemon_Horizons"})
	if got.DownloadID != "41936|[AniDub]_Pokemon_Horizons.torrent|66.16 GB" || got.Miss != "" {
		t.Errorf("got %+v", got)
	}
}

func TestPickByNameOtherBlock(t *testing.T) {
	got := newTracker().PickTorrent(buf(pokemonPage), true, tracker.Held{Name: "[AniDub]_Pokemon_Horizons_100+"})
	if got.DownloadID != "45147|[AniDub]_Pokemon_Horizons_100+.torrent|41.23 GB" {
		t.Errorf("got %+v", got)
	}
}

func TestPickStoredIDWhenNameDiffers(t *testing.T) {
	held := tracker.Held{Name: "old", Stored: true, TopicID: "45147|old.torrent|1 GB"}
	got := newTracker().PickTorrent(buf(pokemonPage), true, held)
	if got.DownloadID != "45147|[AniDub]_Pokemon_Horizons_100+.torrent|41.23 GB" {
		t.Errorf("got %+v", got)
	}
}

func TestPickStoredTitleNewID(t *testing.T) {
	page := `<div id='torrent_45200_info'>Размер: <span class="red">2 GB</span> <span class="red" title="X.torrent">x</span></div>
<div id='torrent_41936_info'>Размер: <span class="red">1 GB</span> <span class="red" title="Y.torrent">x</span></div>`
	held := tracker.Held{Name: "X", Stored: true, TopicID: "41999|X.torrent|1 GB"}
	got := newTracker().PickTorrent(buf(page), true, held)
	if got.DownloadID != "45200|X.torrent|2 GB" {
		t.Errorf("got %+v", got)
	}
}

func TestPickSingleBlock(t *testing.T) {
	got := newTracker().PickTorrent(buf(singlePage), true, tracker.Held{Name: "whatever"})
	if got.DownloadID != "45155|[AniDub]_Ace.of.Diamond.Act.II.s02.torrent|13.74 GB" {
		t.Errorf("got %+v", got)
	}
}

func TestPickAmbiguousMiss(t *testing.T) {
	for name, held := range map[string]tracker.Held{
		"not stored":      {Name: "nothing", TopicID: "45147|x|y"},
		"stored no match": {Name: "nothing", Stored: true, TopicID: "1|none.torrent|2 GB"},
	} {
		got := newTracker().PickTorrent(buf(pokemonPage), true, held)
		if got.Miss == "" || got.DownloadID != "" {
			t.Errorf("%s: got %+v, want Miss only", name, got)
		}
	}
}

func TestPickNoBlocksMiss(t *testing.T) {
	got := newTracker().PickTorrent(buf("<html></html>"), true, tracker.Held{Name: "a"})
	if got.Miss == "" || got.DownloadID != "" {
		t.Errorf("got %+v", got)
	}
}

func TestDownloadLinkComposite(t *testing.T) {
	if got := newTracker().DownloadLink("45155|a|b"); got != "https://tr.anidub.com/engine/download.php?id=45155" {
		t.Errorf("got %q", got)
	}
	if got := newTracker().DownloadLink("45155"); got != "https://tr.anidub.com/engine/download.php?id=45155" {
		t.Errorf("plain id: got %q", got)
	}
}

func TestParseDate(t *testing.T) {
	got := newTracker().ParseDate(buf(pokemonPage), true)
	want := time.Date(2026, time.October, 18, 11, 31, 0, 0, time.FixedZone("MSK", 3*60*60))
	if got == nil || !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseDateToday(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60)
	old := now
	now = func() time.Time { return time.Date(2026, time.October, 22, 0, 30, 0, 0, time.UTC) }
	defer func() { now = old }()
	// 00:30 UTC это уже 03:30 по Москве того же дня
	today := newTracker().ParseDate(buf(`<li><b>Дата:</b> Сегодня, 09:05</li>`), true)
	if want := time.Date(2026, time.October, 22, 9, 5, 0, 0, msk); today == nil || !today.Equal(want) {
		t.Errorf("today: got %v, want %v", today, want)
	}
	yesterday := newTracker().ParseDate(buf(`<li><b>Дата:</b> Вчера, 23:59</li>`), true)
	if want := time.Date(2026, time.October, 21, 23, 59, 0, 0, msk); yesterday == nil || !yesterday.Equal(want) {
		t.Errorf("yesterday: got %v, want %v", yesterday, want)
	}
}

func TestParseDateUnknownNil(t *testing.T) {
	if got := newTracker().ParseDate(buf(`<li><b>Дата:</b> когда-то</li>`), true); got != nil {
		t.Errorf("got %v", got)
	}
}

func TestParsePoster(t *testing.T) {
	page := `<div class="poster_bg ignore-select"><span class="poster"><img src="https://static2.statics.life/tracker/poster/545f53ac70.jpg" alt=""></span></div>`
	if got := newTracker().ParsePoster(buf(page), true); got != "https://static2.statics.life/tracker/poster/545f53ac70.jpg" {
		t.Errorf("got %q", got)
	}
}

func TestParseMagnetEmpty(t *testing.T) {
	if got := newTracker().ParseMagnet(buf(pokemonPage), true); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestPickSiblingSkipsStoredID(t *testing.T) {
	held := tracker.Held{Name: "renamed by user", Stored: true, TopicID: "45147|old.torrent|1 GB", Siblings: []string{"hb"}}
	got := newTracker().PickTorrent(buf(pokemonPage), true, held)
	if got.Miss == "" || got.DownloadID != "" {
		t.Errorf("got %+v, want Miss: stored id may be the sibling's", got)
	}
}

func TestPickSiblingSingleBlockMiss(t *testing.T) {
	got := newTracker().PickTorrent(buf(singlePage), true, tracker.Held{Name: "whatever", Siblings: []string{"hb"}})
	if got.Miss == "" || got.DownloadID != "" {
		t.Errorf("got %+v, want Miss", got)
	}
}

func TestPickSiblingByNameCarriesPageDate(t *testing.T) {
	held := tracker.Held{Name: "[AniDub]_Pokemon_Horizons", Stored: true, TopicID: "45147|[AniDub]_Pokemon_Horizons_100+.torrent|41.23 GB", Siblings: []string{"hb"}}
	got := newTracker().PickTorrent(buf(pokemonPage), true, held)
	want := time.Date(2026, time.October, 18, 11, 31, 0, 0, time.FixedZone("MSK", 3*60*60))
	if got.DownloadID != "41936|[AniDub]_Pokemon_Horizons.torrent|66.16 GB" || got.Date == nil || !got.Date.Equal(want) {
		t.Errorf("got %+v, want own block dated %v", got, want)
	}
}

func TestPickForeignStoredRowMiss(t *testing.T) {
	held := tracker.Held{Name: "[AniDub]_Pokemon_Horizons_old", Stored: true, TopicID: "45147|[AniDub]_Pokemon_Horizons_100+.torrent|41.23 GB"}
	got := newTracker().PickTorrent(buf(pokemonPage), true, held)
	if got.Miss == "" || got.DownloadID != "" {
		t.Errorf("got %+v, want Miss: the row was written by another torrent of the topic", got)
	}
	held.TopicID = "45155|other.torrent|1 GB"
	if got = newTracker().PickTorrent(buf(singlePage), true, held); got.Miss == "" || got.DownloadID != "" {
		t.Errorf("single block: got %+v, want Miss", got)
	}
}
