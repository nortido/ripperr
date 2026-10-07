package booktracker

import (
	"bytes"
	"testing"
	"time"

	"ripperr/internal/model/tracker"
)

const (
	liveMagnet = `<a href="magnet:?xt=urn:btih:A8A27CADE948EC59E1AC8507BA8CB440FEB2E470&tr=http%3A%2F%2Fbt.booktracker.work%2Fann%3Fmagnet" class="magnet-link" data-topic_id="105019">`
	liveHash   = "a8a27cade948ec59e1ac8507ba8cb440feb2e470"
	otherHash  = "0123456789abcdef0123456789abcdef01234567"
	header     = `<a href="viewtopic.php?t=1">Правила</a>`
)

func attachTable(id, inner string) string {
	return `<table class="attach bordered med"><tr><td>` + inner + `<a href="download.php?id=` + id + `">Скачать</a></td></tr></table>`
}

func held(hash string) tracker.Held {
	ts := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)
	return tracker.Held{Hash: hash, Timestamp: &ts}
}

func pick(t *testing.T, page string, h tracker.Held) tracker.Pick {
	t.Helper()
	return New(tracker.Tracker{}).PickTorrent(bytes.NewBufferString(page), true, h)
}

func TestParseMagnet(t *testing.T) {
	tr := New(tracker.Tracker{})
	want := "magnet:?xt=urn:btih:A8A27CADE948EC59E1AC8507BA8CB440FEB2E470&tr=http%3A%2F%2Fbt.booktracker.work%2Fann%3Fmagnet"
	if got := tr.ParseMagnet(bytes.NewBufferString(liveMagnet+` &middot; 597&nbsp;MB`), true); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestParseMagnetUnescapesAmp(t *testing.T) {
	tr := New(tracker.Tracker{})
	page := `<a href="magnet:?xt=urn:btih:` + liveHash + `&amp;dn=x" class="magnet-link">`
	want := "magnet:?xt=urn:btih:" + liveHash + "&dn=x"
	if got := tr.ParseMagnet(bytes.NewBufferString(page), true); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPickDownloadIDSkipsRulesLink(t *testing.T) {
	page := header + attachTable("777", liveMagnet)
	got := pick(t, page, held(liveHash))
	if got.DownloadID != "777" {
		t.Errorf("DownloadID %q, want 777", got.DownloadID)
	}
}

func TestPickUnchangedSameHash(t *testing.T) {
	h := held(liveHash)
	got := pick(t, header+attachTable("777", liveMagnet), h)
	if got.Miss != "" {
		t.Fatalf("unexpected miss %q", got.Miss)
	}
	if got.Date == nil || !got.Date.Equal(*h.Timestamp) {
		t.Errorf("Date %v, want %v", got.Date, h.Timestamp)
	}
}

func TestPickUpdatedOtherHash(t *testing.T) {
	got := pick(t, header+attachTable("777", liveMagnet), held(otherHash))
	if got.Date == nil || time.Since(*got.Date) > time.Minute || time.Until(*got.Date) > time.Minute {
		t.Errorf("Date %v, want about now", got.Date)
	}
	if got.DownloadID != "777" {
		t.Errorf("DownloadID %q, want 777", got.DownloadID)
	}
}

func TestPickTorHashFallback(t *testing.T) {
	page := attachTable("778", `<span id="tor-hash">`+"A8A27CADE948EC59E1AC8507BA8CB440FEB2E470"+`</span>`)
	h := held(liveHash)
	got := pick(t, page, h)
	if got.Date == nil || !got.Date.Equal(*h.Timestamp) {
		t.Errorf("same hash: Date %v, want %v", got.Date, h.Timestamp)
	}
	got = pick(t, page, held(otherHash))
	if got.Date == nil || h.Timestamp.Equal(*got.Date) {
		t.Errorf("other hash: Date %v, want about now", got.Date)
	}
}

func TestPickNoHashMiss(t *testing.T) {
	got := pick(t, header+attachTable("777", ""), held(liveHash))
	if got.Miss != "booktracker: на странице нет хеша раздачи" {
		t.Errorf("Miss %q", got.Miss)
	}
	if got.Date != nil || got.DownloadID != "" {
		t.Errorf("miss carries %v %q", got.Date, got.DownloadID)
	}
}

func TestPickSeveralTablesPicksMatching(t *testing.T) {
	m2 := `<a href="magnet:?xt=urn:btih:` + otherHash + `" class="magnet-link">`
	page := header + attachTable("501", m2) + attachTable("502", liveMagnet)
	h := held(liveHash)
	got := pick(t, page, h)
	if got.DownloadID != "502" {
		t.Errorf("DownloadID %q, want 502", got.DownloadID)
	}
	if got.Date == nil || !got.Date.Equal(*h.Timestamp) {
		t.Errorf("Date %v, want %v", got.Date, h.Timestamp)
	}
}

func TestPickSeveralTablesNoMatchMiss(t *testing.T) {
	m2 := `<a href="magnet:?xt=urn:btih:` + otherHash + `" class="magnet-link">`
	page := attachTable("501", m2) + attachTable("502", liveMagnet)
	got := pick(t, page, held("ffffffffffffffffffffffffffffffffffffffff"))
	if got.Miss == "" || got.Date != nil {
		t.Errorf("want miss, got %+v", got)
	}
}

func TestPickGuestPageWithoutAttachTable(t *testing.T) {
	h := held(liveHash)
	got := pick(t, header+liveMagnet, h)
	if got.Miss != "" || got.DownloadID != "" {
		t.Fatalf("got %+v", got)
	}
	if got.Date == nil || !got.Date.Equal(*h.Timestamp) {
		t.Errorf("Date %v, want %v", got.Date, h.Timestamp)
	}
}

func TestPickNestedTableStaysInside(t *testing.T) {
	page := `<table class="attach"><tr><td><table><tr><td>` + liveMagnet + `</td></tr></table><a href="download.php?id=321">x</a></td></tr></table>`
	got := pick(t, page, held(liveHash))
	if got.DownloadID != "321" {
		t.Errorf("DownloadID %q, want 321", got.DownloadID)
	}
}

func TestPickNoClientHashMiss(t *testing.T) {
	got := pick(t, attachTable("777", liveMagnet), held(""))
	if got.Miss == "" {
		t.Errorf("want miss without client hash, got %+v", got)
	}
}

func TestParseDateStaysNil(t *testing.T) {
	tr := New(tracker.Tracker{})
	if got := tr.ParseDate(bytes.NewBufferString(`Зарегистрирован &nbsp; [ <span title="10 часов">2026-09-01 11:11</span> ]`), true); got != nil {
		t.Errorf("date parsed: %v", got)
	}
}

func TestTopicLink(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "booktracker.org"})
	if got := tr.TopicLink("105019"); got != "https://booktracker.org/viewtopic.php?t=105019" {
		t.Errorf("got %q", got)
	}
}

func TestParsePoster(t *testing.T) {
	tr := New(tracker.Tracker{})
	page := `<a href="https://postimages.org/" class="postLink"><var class="postImg" title="https://i.postimg.cc/WzF609vt/Krov'-3.jpg">&#10;</var></a>`
	if got := tr.ParsePoster(bytes.NewBufferString(page), true); got != "https://i.postimg.cc/WzF609vt/Krov'-3.jpg" {
		t.Errorf("got %q", got)
	}
}

func TestPickUpdatedWithoutDownloadIDMiss(t *testing.T) {
	got := pick(t, header+liveMagnet, held(otherHash))
	if got.Miss == "" || got.Date != nil || got.DownloadID != "" {
		t.Errorf("got %+v, want Miss: update without a download id", got)
	}
}

func TestPickPageWithSeveralMagnetsNoneHeldMiss(t *testing.T) {
	m2 := `<a href="magnet:?xt=urn:btih:` + otherHash + `" class="magnet-link">`
	got := pick(t, header+m2+liveMagnet+`<a href="download.php?id=777">x</a>`, held("ffffffffffffffffffffffffffffffffffffffff"))
	if got.Miss == "" || got.Date != nil {
		t.Errorf("got %+v, want Miss: several magnets, none held", got)
	}
}

func TestPickPageWithSeveralMagnetsHeldIsUnchanged(t *testing.T) {
	m2 := `<a href="magnet:?xt=urn:btih:` + otherHash + `" class="magnet-link">`
	h := held(liveHash)
	got := pick(t, header+m2+liveMagnet, h)
	if got.Miss != "" || got.Date == nil || !got.Date.Equal(*h.Timestamp) {
		t.Errorf("got %+v, want unchanged", got)
	}
}

func TestPickMagnetOutsideAttachTableMiss(t *testing.T) {
	m2 := `<a href="magnet:?xt=urn:btih:` + otherHash + `" class="magnet-link">`
	got := pick(t, header+m2+attachTable("777", liveMagnet), held("ffffffffffffffffffffffffffffffffffffffff"))
	if got.Miss == "" || got.Date != nil {
		t.Errorf("got %+v, want Miss: first magnet of the page is not the attach one", got)
	}
}

func TestPickSiblingHeldGoneMiss(t *testing.T) {
	h := held(otherHash)
	h.Siblings = []string{"ffffffffffffffffffffffffffffffffffffffff"}
	got := pick(t, header+attachTable("777", liveMagnet), h)
	if got.Miss == "" || got.Date != nil {
		t.Errorf("got %+v, want Miss: the page torrent may be the sibling's", got)
	}
	h = held(liveHash)
	h.Siblings = []string{otherHash}
	got = pick(t, header+attachTable("777", liveMagnet), h)
	if got.Miss != "" || got.Date == nil || !got.Date.Equal(*h.Timestamp) {
		t.Errorf("got %+v, want unchanged: held hash is on the page", got)
	}
}
