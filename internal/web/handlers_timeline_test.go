package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"ripperr/internal/model/storage"
	"ripperr/internal/notify"
)

var timelineNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func tlDay(n int) time.Time { return timelineNow.AddDate(0, 0, n) }

func tlRFC(t time.Time) string { return t.Format(time.RFC3339) }

func tlUpdated(id int64, at time.Time) storage.EventEntry {
	return storage.EventEntry{ID: id, At: at, Kind: notify.KindUpdated, Subject: "Show"}
}

func tlFailed(id int64, at time.Time, text string) storage.EventEntry {
	return storage.EventEntry{ID: id, At: at, Kind: notify.KindCheckFailed, Subject: "Show", Text: text}
}

func tlCheck(t *testing.T, got []timelineEntryResp, want []timelineEntryResp) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("entries:\n got %+v\nwant %+v", got, want)
	}
}

func TestBuildTimelinePairsTrackerTimes(t *testing.T) {
	d0 := tlDay(-10)
	t1, t2 := tlDay(-9), tlDay(-6)
	e1, e2 := tlDay(-8), tlDay(-5)
	resp := buildTimeline(
		storage.ReleaseInfo{Name: "Show", Status: "ok", AddedAt: &d0},
		[]time.Time{t2, t1, d0.AddDate(0, 0, -2)},
		[]storage.EventEntry{tlUpdated(1, e1), tlUpdated(2, e2)},
		timelineNow,
	)
	tlCheck(t, resp.Entries, []timelineEntryResp{
		{Kind: "updated", At: tlRFC(e2), ReleasedAt: tlRFC(t2)},
		{Kind: "updated", At: tlRFC(e1), ReleasedAt: tlRFC(t1)},
		{Kind: "added", At: tlRFC(d0), ReleasedAt: tlRFC(d0.AddDate(0, 0, -2))},
	})
	if resp.UpdateCount != 2 || resp.PeriodDays != 30 {
		t.Fatalf("count %d period %d", resp.UpdateCount, resp.PeriodDays)
	}
}

func TestBuildTimelineExtraTrackerTime(t *testing.T) {
	ta, tb := tlDay(-9), tlDay(-8)
	e1, e2 := tlDay(-7), tlDay(-4)
	resp := buildTimeline(
		storage.ReleaseInfo{Name: "Show", Status: "ok"},
		[]time.Time{tb, ta},
		[]storage.EventEntry{tlUpdated(1, e1), tlUpdated(2, e2)},
		timelineNow,
	)
	tlCheck(t, resp.Entries, []timelineEntryResp{
		{Kind: "updated", At: tlRFC(e2)},
		{Kind: "updated", At: tlRFC(e1), ReleasedAt: tlRFC(tb)},
		{Kind: "tracker", At: tlRFC(ta)},
	})
	if resp.UpdateCount != 3 {
		t.Fatalf("count %d", resp.UpdateCount)
	}
}

func TestBuildTimelineOldTrackerTime(t *testing.T) {
	old := tlDay(-60)
	resp := buildTimeline(storage.ReleaseInfo{Name: "Show", Status: "ok"}, []time.Time{old}, nil, timelineNow)
	tlCheck(t, resp.Entries, []timelineEntryResp{{Kind: "tracker", At: tlRFC(old)}})
	if resp.UpdateCount != 1 || resp.PeriodDays != 90 {
		t.Fatalf("count %d period %d", resp.UpdateCount, resp.PeriodDays)
	}
}

func TestBuildTimelineCheckFailed(t *testing.T) {
	events := []storage.EventEntry{
		tlFailed(2, tlDay(0).Add(-time.Hour), "Release check failed: boom"),
		tlFailed(1, tlDay(0).Add(-2*time.Hour), "Ошибка проверки раздачи: timeout"),
		tlFailed(3, tlDay(0).Add(-3*time.Hour), "Release check failed: boom"),
	}
	info := storage.ReleaseInfo{Name: "Show", Status: "error", Error: " boom\n"}
	resp := buildTimeline(info, nil, events, timelineNow)
	tlCheck(t, resp.Entries, []timelineEntryResp{
		{Kind: "check_failed", At: tlRFC(tlDay(0).Add(-2 * time.Hour)), Text: "timeout"},
		{Kind: "check_failed", At: tlRFC(tlDay(0).Add(-3 * time.Hour)), Text: "boom"},
	})
	if resp.UpdateCount != 0 {
		t.Fatalf("count %d", resp.UpdateCount)
	}

	info.Status = "unknown"
	resp = buildTimeline(info, nil, events, timelineNow)
	if len(resp.Entries) != 3 {
		t.Fatalf("unknown status must drop nothing, got %+v", resp.Entries)
	}
}

func TestBuildTimelineEmpty(t *testing.T) {
	resp := buildTimeline(storage.ReleaseInfo{Name: "Show", Status: "ok"}, nil, nil, timelineNow)
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"entries":[]`) {
		t.Fatalf("entries must be an empty array: %s", raw)
	}
	if resp.UpdateCount != 0 || resp.PeriodDays != 30 || resp.CheckedAt != "" || resp.RecheckAt != "" {
		t.Fatalf("unexpected header: %+v", resp)
	}
}

func TestReleaseTimelineHTTP(t *testing.T) {
	checked := time.Date(2026, 10, 30, 11, 0, 0, 0, time.UTC)
	fs := &fakeStore{releaseInfo: map[string]storage.ReleaseInfo{
		"123": {Name: "Show", HistoryName: "Old", Status: "ok", CheckedAt: &checked},
	}}
	fe := &fakeEvents{items: []storage.EventEntry{
		{ID: 1, At: tlDay(-1), Kind: notify.KindUpdated, Subject: "Show"},
		{ID: 2, At: tlDay(-2), Kind: notify.KindUpdated, Subject: "Other"},
	}}
	srv := withTestSession(newBareServer(fs))
	srv.SetEvents(fe)

	rec := doEvents(srv, "GET", "/api/history/123/timeline", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var got timelineResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Show" || got.Status != "ok" || got.CheckedAt != tlRFC(checked) || got.RecheckAt != "" {
		t.Fatalf("unexpected body: %+v", got)
	}
	if len(got.Entries) != 1 || got.Entries[0].Kind != "updated" {
		t.Fatalf("entries: %+v", got.Entries)
	}
	if !slices.Equal(fe.lastSubjects, []string{"Show", "Old"}) || !slices.Equal(fe.lastKinds, []string{notify.KindUpdated, notify.KindCheckFailed}) {
		t.Fatalf("subjects %v kinds %v", fe.lastSubjects, fe.lastKinds)
	}

	if rec = doEvents(srv, "GET", "/api/history/999/timeline", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
	if rec = doEvents(srv, "GET", "/api/history/abc/timeline", "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad hash: %d", rec.Code)
	}

	noEvents := withTestSession(newBareServer(fs))
	rec = doEvents(noEvents, "GET", "/api/history/123/timeline", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"entries":[]`) {
		t.Fatalf("no events: %d %s", rec.Code, rec.Body)
	}

	fs.releaseErr = errors.New("db down")
	if rec = doEvents(srv, "GET", "/api/history/123/timeline", "", nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("store error: %d", rec.Code)
	}
}

func TestBuildTimelineFirstCheckUpdatesOutdatedTorrent(t *testing.T) {
	d0 := tlDay(-10)
	t1 := tlDay(-11)
	e1 := tlDay(-9)
	resp := buildTimeline(
		storage.ReleaseInfo{Name: "Show", Status: "ok", AddedAt: &d0},
		[]time.Time{t1},
		[]storage.EventEntry{tlUpdated(1, e1)},
		timelineNow,
	)
	tlCheck(t, resp.Entries, []timelineEntryResp{
		{Kind: "updated", At: tlRFC(e1), ReleasedAt: tlRFC(t1)},
		{Kind: "added", At: tlRFC(d0)},
	})
}
