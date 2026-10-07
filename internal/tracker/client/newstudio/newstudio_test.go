package newstudio

import (
	"bytes"
	"testing"
	"time"

	"ripperr/internal/model/tracker"
)

const (
	liveHash     = "567d9de0af20c4a4d5efbd5f983d360bfb1e1ee2"
	otherHash    = "0123456789abcdef0123456789abcdef01234567"
	thirdHash    = "89abcdef0123456789abcdef0123456789abcdef"
	liveMagnet   = `<a href="magnet:?xt=urn:btih:567d9de0af20c4a4d5efbd5f983d360bfb1e1ee2&tr=http%3A%2F%2Fnewstudiogroup.tv%3A2710%2Fannounce%3Fuk%3DPWx5KQczBA"><i class="icon-download-alt"></i></a>`
	liveDownload = `<a href="download.php?id=48190" class="genmed">Hatton.Garden.S01.WEBDL.1080p.NewStudio.TV.torrent</a>`
	livePoster   = `<var class="postImg" title="http://i124.fastpic.org/big/2024/0913/7c/51e47f9faa04ddd962382e81646be27c.jpg"></var>`
	livePage     = liveMagnet + liveDownload
	liveURL      = "https://newstudio.tv:443/viewtopic.php?p=153133"
)

func magnetOf(hash string) string {
	return `<a href="magnet:?xt=urn:btih:` + hash + `&amp;tr=http%3A%2F%2Fnewstudiogroup.tv%3A2710%2Fannounce"></a>`
}

func held(hash string) tracker.Held {
	ts := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)
	return tracker.Held{Hash: hash, TopicID: "443", URL: liveURL, Timestamp: &ts}
}

func pick(page string, h tracker.Held) tracker.Pick {
	return New(tracker.Tracker{}).PickTorrent(bytes.NewBufferString(page), false, h)
}

func TestPickUnchangedSameHash(t *testing.T) {
	h := held("567D9DE0AF20C4A4D5EFBD5F983D360BFB1E1EE2")
	got := pick(livePage, h)
	if got.Miss != "" {
		t.Fatalf("Miss %q", got.Miss)
	}
	if got.Date != h.Timestamp {
		t.Errorf("Date %v, want held.Timestamp", got.Date)
	}
	if got.DownloadID != "48190" {
		t.Errorf("DownloadID %q, want 48190", got.DownloadID)
	}
}

func TestPickUpdatedOtherHash(t *testing.T) {
	start := time.Now()
	got := pick(livePage, held(otherHash))
	if got.Miss != "" {
		t.Fatalf("Miss %q", got.Miss)
	}
	if got.DownloadID != "48190" {
		t.Errorf("DownloadID %q, want 48190 (not the port 443)", got.DownloadID)
	}
	if got.Date == nil || got.Date.Before(start) {
		t.Errorf("Date %v, want now", got.Date)
	}
}

func TestPickNoHashMiss(t *testing.T) {
	got := pick(liveDownload, held(liveHash))
	if got.Miss == "" || got.DownloadID != "" || got.Date != nil {
		t.Errorf("want Miss only: %+v", got)
	}
}

func TestPickNoClientHashMiss(t *testing.T) {
	got := pick(livePage, held(""))
	if got.Miss == "" || got.DownloadID != "" || got.Date != nil {
		t.Errorf("want Miss only: %+v", got)
	}
}

func TestPickSeveralHashesNoneHeldMiss(t *testing.T) {
	page := magnetOf(otherHash) + magnetOf(thirdHash) + liveDownload
	got := pick(page, held(liveHash))
	if got.Miss == "" || got.DownloadID != "" || got.Date != nil {
		t.Errorf("want Miss only: %+v", got)
	}
}

func TestPickSeveralHashesHeldUnchanged(t *testing.T) {
	page := magnetOf(otherHash) + magnetOf(liveHash) + liveDownload
	h := held(liveHash)
	got := pick(page, h)
	if got.Miss != "" || got.Date != h.Timestamp {
		t.Errorf("want unchanged: %+v", got)
	}
}

func TestPickSiblingOwnAbsentMiss(t *testing.T) {
	h := held(otherHash)
	h.Siblings = []string{thirdHash}
	got := pick(livePage, h)
	if got.Miss == "" || got.DownloadID != "" || got.Date != nil {
		t.Errorf("want Miss only: %+v", got)
	}
}

func TestPickUpdatedWithoutDownloadIDMiss(t *testing.T) {
	got := pick(liveMagnet, held(otherHash))
	if got.Miss == "" || got.DownloadID != "" || got.Date != nil {
		t.Errorf("want Miss only: %+v", got)
	}
}

func TestPickTwoDownloadLinksMiss(t *testing.T) {
	page := livePage + `<a href="./download.php?id=48191">x</a>`
	got := pick(page, held(otherHash))
	if got.Miss == "" || got.DownloadID != "" || got.Date != nil {
		t.Errorf("want Miss only: %+v", got)
	}
}

func TestPickUnchangedWithoutDownloadID(t *testing.T) {
	h := held(liveHash)
	got := pick(liveMagnet, h)
	if got.Miss != "" || got.Date != h.Timestamp || got.DownloadID != "" {
		t.Errorf("want unchanged without id: %+v", got)
	}
}

func TestParsePoster(t *testing.T) {
	tr := New(tracker.Tracker{})
	want := "http://i124.fastpic.org/big/2024/0913/7c/51e47f9faa04ddd962382e81646be27c.jpg"
	if got := tr.ParsePoster(bytes.NewBufferString(livePoster), true); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDetect(t *testing.T) {
	tr := New(tracker.Tracker{Domain: "newstudio.tv"})
	if !tr.Detect(liveURL) {
		t.Error("comment url must be detected")
	}
	if tr.Detect("http://newstudiogroup.tv:2710/announce") {
		t.Error("announce host must not be detected")
	}
}
