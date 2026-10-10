package runner

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"ripperr/internal/model"
	runnerModel "ripperr/internal/model/runner"
	storageModel "ripperr/internal/model/storage"
	storagePkg "ripperr/internal/storage"
)

func TestTopicBackoffGrowsAndCaps(t *testing.T) {
	cases := []struct {
		streak int
		slot   time.Duration
		want   time.Duration
	}{
		{0, 12 * time.Hour, 0},
		{1, 12 * time.Hour, 0},
		{2, 12 * time.Hour, 12 * time.Hour},
		{3, 12 * time.Hour, 24 * time.Hour},
		{3, time.Hour, 3 * time.Hour},
		{4, time.Hour, 7 * time.Hour},
		{5, time.Hour, 15 * time.Hour},
		{6, time.Hour, 24 * time.Hour},
		{62, time.Hour, 24 * time.Hour},
		{63, time.Hour, 24 * time.Hour},
		{64, time.Hour, 24 * time.Hour},
		{1000, time.Hour, 24 * time.Hour},
		{math.MaxInt, time.Hour, 24 * time.Hour},
		{math.MaxInt, time.Nanosecond, 24 * time.Hour},
		{3, 48 * time.Hour, 24 * time.Hour},
	}
	for _, c := range cases {
		if got := topicBackoff(c.streak, c.slot); got != c.want {
			t.Errorf("topicBackoff(%d, %v) = %v, want %v", c.streak, c.slot, got, c.want)
		}
	}
}

func TestTopicBackoffNonPositiveSlot(t *testing.T) {
	for _, slot := range []time.Duration{0, -time.Hour} {
		if got := topicBackoff(5, slot); got != 0 {
			t.Errorf("topicBackoff(5, %v) = %v, want 0", slot, got)
		}
	}
}

func TestInBackoffWindow(t *testing.T) {
	now := time.Now()
	slot := 12 * time.Hour
	cases := []struct {
		name string
		m    storageModel.ErrorMark
		want bool
	}{
		{"inside", storageModel.ErrorMark{Streak: 2, CheckedAt: now.Add(-time.Hour)}, true},
		{"at start", storageModel.ErrorMark{Streak: 2, CheckedAt: now}, true},
		{"at end", storageModel.ErrorMark{Streak: 2, CheckedAt: now.Add(-slot)}, false},
		{"future checked_at", storageModel.ErrorMark{Streak: 2, CheckedAt: now.Add(time.Hour)}, false},
		{"zero checked_at", storageModel.ErrorMark{Streak: 2}, false},
		{"streak 1", storageModel.ErrorMark{Streak: 1, CheckedAt: now.Add(-time.Hour)}, false},
	}
	for _, c := range cases {
		if got := inBackoff(now, c.m, slot); got != c.want {
			t.Errorf("%s: inBackoff = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEnqueueSkipsTopicInErrorBackoff(t *testing.T) {
	const url = "https://example.com/t/1"
	hash := storagePkg.URLHash(url)
	mark := func(ago time.Duration) map[string]storageModel.ErrorMark {
		return map[string]storageModel.ErrorMark{hash: {Streak: 2, CheckedAt: time.Now().Add(-ago)}}
	}
	cases := []struct {
		name   string
		st     *fakeStorage
		dry    bool
		forced bool
		queued bool
	}{
		{name: "inside window", st: &fakeStorage{marks: mark(time.Hour)}},
		{name: "after window", st: &fakeStorage{marks: mark(13 * time.Hour)}, queued: true},
		{name: "force_check item", st: &fakeStorage{marks: mark(time.Hour), items: map[string]*runnerModel.HistoryItem{
			hash: {URLHash: hash, Exists: true, TrackerTypeID: 1, ForceCheck: true},
		}}, queued: true},
		{name: "forced pass", st: &fakeStorage{marks: mark(time.Hour)}, forced: true, queued: true},
		{name: "dry run", st: &fakeStorage{marks: mark(time.Hour)}, dry: true, queued: true},
		{name: "marks error", st: &fakeStorage{marks: mark(time.Hour), marksErr: errors.New("boom")}, queued: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &handler{
				config:         model.HandlerConfig{ListRefreshPerDay: 2},
				manager:        &fakeManager{all: []*model.Entry{{URL: url, Name: "x"}}},
				storage:        c.st,
				trackerService: &fakeTracker{id: 1},
			}
			h.initTrackerChannels()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var forced map[string]struct{}
			queue := h.idx[1]
			if c.forced {
				forced = map[string]struct{}{hash: {}}
				queue = h.pri[1]
			}
			if err := h.push(ctx, c.dry, forced); err != nil {
				t.Fatal(err)
			}
			select {
			case <-queue:
				if !c.queued {
					t.Fatal("topic in error backoff must not be queued")
				}
			default:
				if c.queued {
					t.Fatal("topic must be queued")
				}
			}
		})
	}
}
