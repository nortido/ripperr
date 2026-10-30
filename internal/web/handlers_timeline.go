package web

import (
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"ripperr/internal/model/storage"
	"ripperr/internal/notify"
	"ripperr/internal/system/logger"
)

const (
	timelineReleaseWindow = 90 * 24 * time.Hour
	timelineEventsWindow  = 30 * 24 * time.Hour
	timelineReleaseTimes  = 8
	timelineEventsLimit   = 200
)

func (s *Server) handleReleaseTimeline(w http.ResponseWriter, r *http.Request) {
	hash, ok := releaseHash(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	info, found, err := s.db.ReleaseInfo(ctx, hash)
	if err != nil {
		logger.Warn("release timeline: info: " + err.Error())
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "release not found")
		return
	}
	now := time.Now()
	released, err := s.db.RecentReleaseTimes(ctx, hash, now.Add(-timelineReleaseWindow), timelineReleaseTimes)
	if err != nil {
		logger.Warn("release timeline: release times: " + err.Error())
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	var events []storage.EventEntry
	if s.events != nil {
		// события привязаны к имени релиза, а не к хэшу: после переименования старые не находятся,
		// а релиз с тем же именем (тема пересоздана) может подмешать чужие записи
		var subjects []string
		for _, n := range []string{info.Name, info.HistoryName} {
			if n != "" && !slices.Contains(subjects, n) {
				subjects = append(subjects, n)
			}
		}
		events, err = s.events.ListSubjectEvents(ctx, subjects, []string{notify.KindUpdated, notify.KindCheckFailed}, timelineEventsLimit)
		if err != nil {
			logger.Warn("release timeline: events: " + err.Error())
			writeError(w, http.StatusInternalServerError, "storage error")
			return
		}
	}
	writeJSON(w, http.StatusOK, buildTimeline(info, released, events, now))
}

type timelineAnchor struct {
	at    time.Time
	id    int64
	added bool
}

type timelineItem struct {
	at    time.Time
	entry timelineEntryResp
}

func buildTimeline(info storage.ReleaseInfo, released []time.Time, events []storage.EventEntry, now time.Time) timelineResp {
	resp := timelineResp{
		Name:       info.Name,
		Status:     info.Status,
		Error:      info.Error,
		PeriodDays: 30,
		Entries:    []timelineEntryResp{},
	}
	if info.CheckedAt != nil {
		resp.CheckedAt = info.CheckedAt.Format(time.RFC3339)
	}
	if info.RecheckAt != nil {
		resp.RecheckAt = info.RecheckAt.Format(time.RFC3339)
	}

	var anchors []timelineAnchor
	if info.AddedAt != nil {
		anchors = append(anchors, timelineAnchor{at: *info.AddedAt, id: -1, added: true})
	}
	var failed []storage.EventEntry
	for _, e := range events {
		switch e.Kind {
		case notify.KindUpdated:
			anchors = append(anchors, timelineAnchor{at: e.At, id: e.ID})
		case notify.KindCheckFailed:
			failed = append(failed, e)
		}
	}
	sort.SliceStable(anchors, func(i, j int) bool {
		if !anchors[i].at.Equal(anchors[j].at) {
			return anchors[i].at.Before(anchors[j].at)
		}
		return anchors[i].id < anchors[j].id
	})

	times := slices.Clone(released)
	sort.Slice(times, func(i, j int) bool { return times[i].After(times[j]) })
	used := make([]bool, len(times))
	var items []timelineItem
	// сначала замены: версия, поставленная при первой проверке, старше AddedAt, и добавление не должно забрать ее время
	sort.SliceStable(anchors, func(i, j int) bool { return !anchors[i].added && anchors[j].added })
	var prev time.Time
	hasPrev := false
	for _, a := range anchors {
		entry := timelineEntryResp{Kind: "updated", At: a.at.Format(time.RFC3339)}
		if a.added {
			entry.Kind = "added"
		}
		for k, r := range times {
			if used[k] || r.After(a.at) || (!a.added && hasPrev && !r.After(prev)) {
				continue
			}
			used[k] = true
			entry.ReleasedAt = r.Format(time.RFC3339)
			break
		}
		if !a.added {
			prev, hasPrev = a.at, true
		}
		items = append(items, timelineItem{a.at, entry})
	}
	for k, r := range times {
		if !used[k] {
			items = append(items, timelineItem{r, timelineEntryResp{Kind: "tracker", At: r.Format(time.RFC3339)}})
		}
	}

	drop := -1
	if want := strings.TrimSpace(info.Error); info.Status == "error" && want != "" {
		for i, e := range failed {
			if strings.TrimSpace(notify.CheckFailedReason(e.Text)) != want {
				continue
			}
			if drop < 0 || e.At.After(failed[drop].At) || (e.At.Equal(failed[drop].At) && e.ID > failed[drop].ID) {
				drop = i
			}
		}
	}
	for i, e := range failed {
		if i != drop {
			items = append(items, timelineItem{e.At, timelineEntryResp{Kind: "check_failed", At: e.At.Format(time.RFC3339), Text: notify.CheckFailedReason(e.Text)}})
		}
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].at.After(items[j].at) })
	cutoff := now.Add(-timelineEventsWindow)
	for _, it := range items {
		resp.Entries = append(resp.Entries, it.entry)
		if it.entry.Kind == "updated" || it.entry.Kind == "tracker" {
			resp.UpdateCount++
			if it.at.Before(cutoff) {
				resp.PeriodDays = 90
			}
		}
	}
	return resp
}
