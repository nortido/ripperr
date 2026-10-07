package selezen

import (
	"bytes"
	"testing"
	"time"

	"ripperr/internal/model/tracker"
)

const hashA = "a8a27cad0d1d6a1a3d6f1d9f2d3a8b6d3c4e5f60"

const loggedPage = `<div id="dwt"><a href="magnet:?xt=urn:btih:A8A27CAD0D1D6A1A3D6F1D9F2D3A8B6D3C4E5F60&amp;dn=Florist&amp;tr=udp://t:1">Скачать</a></div>`

const guestPage = `<div id="dwt"><h4> Авторизуйтесь для получения доступа к скачиванию </h4></div>`

func buf(s string) *bytes.Buffer { return bytes.NewBufferString(s) }

func newTracker() *Tracker {
	return New(tracker.Tracker{Domain: "selezen.top", Mirrors: []string{"www.selezen.club"}})
}

func TestDetectMirrors(t *testing.T) {
	tr := newTracker()
	for _, u := range []string{
		"https://selezen.org/relizy/1-a.html",
		"https://open.selezen.top/relizy/1-a.html",
		"https://use.selezen.club/relizy/1-a.html",
		"https://selezen.top/relizy/1-a.html",
		"http://mel.selezen.net/relizy/1-a.html",
		"https://s1.selezen.site/relizy/1-a.html",
	} {
		if !tr.Detect(u) {
			t.Errorf("Detect(%q) = false", u)
		}
	}
	for _, u := range []string{"https://example.org/relizy/1-a.html"} {
		if tr.Detect(u) {
			t.Errorf("Detect(%q) = true", u)
		}
	}
}

func TestPickUnchangedSameHash(t *testing.T) {
	stamp := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC)
	got := newTracker().PickTorrent(buf(loggedPage), true, tracker.Held{Hash: hashA, Timestamp: &stamp})
	if got.Miss != "" || got.Date == nil || !got.Date.Equal(stamp) {
		t.Errorf("got %+v, want Date == %v", got, stamp)
	}
}

func TestPickUpdatedOtherHash(t *testing.T) {
	stamp := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC)
	before := time.Now()
	got := newTracker().PickTorrent(buf(loggedPage), true, tracker.Held{Hash: "ffffffffffffffffffffffffffffffffffffffff", Timestamp: &stamp})
	if got.Miss != "" || got.Date == nil || got.Date.Before(before) {
		t.Errorf("got %+v, want Date >= %v", got, before)
	}
}

func TestPickGuestMiss(t *testing.T) {
	got := newTracker().PickTorrent(buf(guestPage), true, tracker.Held{Hash: hashA})
	if got.Miss != "selezen: на странице нет magnet-ссылки, нужен вход" || got.DownloadID != "" || got.Date != nil {
		t.Errorf("got %+v", got)
	}
}

func TestParseMagnet(t *testing.T) {
	want := "magnet:?xt=urn:btih:A8A27CAD0D1D6A1A3D6F1D9F2D3A8B6D3C4E5F60&dn=Florist&tr=udp://t:1"
	if got := newTracker().ParseMagnet(buf(loggedPage), true); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := newTracker().ParseMagnet(buf(guestPage), true); got != "" {
		t.Errorf("guest: got %q", got)
	}
}

func TestDownloadLinkEmpty(t *testing.T) {
	if got := newTracker().DownloadLink("13162"); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestParseDateStaysNil(t *testing.T) {
	page := `"edit": 0,  "edit_date": "21.10.2026 18:09",  "news_id": "13162"`
	if got := newTracker().ParseDate(buf(page), true); got != nil {
		t.Errorf("got %v", got)
	}
}

func TestParsePoster(t *testing.T) {
	page := `<meta property="og:image" content="https://poster.desol.one/posts/2026-10/266415ec98_cover.webp" />`
	if got := newTracker().ParsePoster(buf(page), true); got != "https://poster.desol.one/posts/2026-10/266415ec98_cover.webp" {
		t.Errorf("got %q", got)
	}
}

const hashB = "0123456789abcdef0123456789abcdef01234567"

const twoMagnets = `<div class="comment"><a href="magnet:?xt=urn:btih:` + hashB + `&amp;dn=Other">x</a></div>` + loggedPage

func TestPickSeveralMagnetsNoneHeldMiss(t *testing.T) {
	stamp := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC)
	got := newTracker().PickTorrent(buf(twoMagnets), true, tracker.Held{Hash: "ffffffffffffffffffffffffffffffffffffffff", Timestamp: &stamp})
	if got.Miss == "" || got.Date != nil {
		t.Errorf("got %+v, want Miss", got)
	}
}

func TestPickSeveralMagnetsHeldIsUnchanged(t *testing.T) {
	stamp := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC)
	got := newTracker().PickTorrent(buf(twoMagnets), true, tracker.Held{Hash: hashA, Timestamp: &stamp})
	if got.Miss != "" || got.Date == nil || !got.Date.Equal(stamp) {
		t.Errorf("got %+v, want Date == %v", got, stamp)
	}
}

func TestPickSameMagnetTwiceIsOne(t *testing.T) {
	stamp := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC)
	got := newTracker().PickTorrent(buf(loggedPage+loggedPage), true, tracker.Held{Hash: "ffffffffffffffffffffffffffffffffffffffff", Timestamp: &stamp})
	if got.Miss != "" || got.Date == nil || got.Date.Equal(stamp) {
		t.Errorf("got %+v, want an update", got)
	}
}

func TestPickSiblingHeldGoneMiss(t *testing.T) {
	stamp := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC)
	got := newTracker().PickTorrent(buf(loggedPage), true, tracker.Held{Hash: "ffffffffffffffffffffffffffffffffffffffff", Timestamp: &stamp, Siblings: []string{hashA}})
	if got.Miss == "" || got.Date != nil {
		t.Errorf("got %+v, want Miss", got)
	}
}
