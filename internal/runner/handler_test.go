package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"ripperr/internal/helper"
	"ripperr/internal/metrics"
	"ripperr/internal/model"
	proxyModel "ripperr/internal/model/proxy"
	runnerModel "ripperr/internal/model/runner"
	storageModel "ripperr/internal/model/storage"
	"ripperr/internal/model/tracker"
	"ripperr/internal/notify"
	storagePkg "ripperr/internal/storage"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker/client"
	"ripperr/internal/tracker/registry"
)

func timePtr(t time.Time) *time.Time { return &t }

type fakeStorage struct {
	ignore      map[string]struct{}
	items       map[string]*runnerModel.HistoryItem
	deleted     []string
	checkStreak int
	ignoreCalls []string
	marks       map[string]storageModel.ErrorMark
	marksErr    error
	releases    []releaseCall
	releaseErr  error
	recent      []time.Time
	recentErr   error
	recentCalls []recentCall
	purges      []map[string][]string
	holds       map[string]time.Time
	holdsErr    error
	holdsCalls  int
	droppedNew  []string
	knownHashes map[string]struct{}
	knownNames  []string
	knownErr    error
	newAdds     []newAdd
	newAddErr   error
	listedNames map[string]string
	listedFresh map[string]struct{}
	listedErr   error
}

type newAdd struct {
	entry storageModel.CheckLogEntry
	at    time.Time
}

type recentCall struct {
	hash  string
	since time.Time
	limit int
}

type releaseCall struct {
	hash string
	at   time.Time
	keep int
}

func (f *fakeStorage) AddReleaseTime(_ context.Context, urlHash string, at time.Time, keep int) error {
	f.releases = append(f.releases, releaseCall{hash: urlHash, at: at, keep: keep})
	return f.releaseErr
}

func (f *fakeStorage) RecentReleaseTimes(_ context.Context, urlHash string, since time.Time, limit int) ([]time.Time, error) {
	f.recentCalls = append(f.recentCalls, recentCall{hash: urlHash, since: since, limit: limit})
	return f.recent, f.recentErr
}

func (f *fakeStorage) SaveItem(context.Context, *runnerModel.HistoryItem) error { return nil }

func (f *fakeStorage) AddCheckLog(context.Context, *storageModel.CheckLogEntry) (int, error) {
	return f.checkStreak, nil
}

func (f *fakeStorage) AddIgnore(_ context.Context, hash, _, _ string) error {
	f.ignoreCalls = append(f.ignoreCalls, hash)
	return nil
}

func (f *fakeStorage) AllItems(context.Context) (map[string]*runnerModel.HistoryItem, error) {
	out := make(map[string]*runnerModel.HistoryItem, len(f.items))
	for _, it := range f.items {
		out[it.URLHash] = it
	}
	return out, nil
}

func (f *fakeStorage) GetIgnoreSet(context.Context) (map[string]struct{}, error) {
	return f.ignore, nil
}

func (f *fakeStorage) DeleteItem(_ context.Context, urlHash string) error {
	f.deleted = append(f.deleted, urlHash)
	return nil
}

func (f *fakeStorage) NewReleaseHolds(context.Context) (map[string]time.Time, error) {
	f.holdsCalls++
	return f.holds, f.holdsErr
}

func (f *fakeStorage) DeleteNewRelease(_ context.Context, urlHash string) error {
	f.droppedNew = append(f.droppedNew, urlHash)
	return nil
}

func (f *fakeStorage) KnownReleases(context.Context) (hashes, names map[string]struct{}, err error) {
	if f.knownErr != nil {
		return nil, nil, f.knownErr
	}
	hashes = map[string]struct{}{}
	for k := range f.knownHashes {
		hashes[k] = struct{}{}
	}
	names = map[string]struct{}{}
	for _, n := range f.knownNames {
		names[storageModel.FoldName(n)] = struct{}{}
	}
	return hashes, names, nil
}

func (f *fakeStorage) ListedReleases(context.Context) (names map[string]string, fresh map[string]struct{}, err error) {
	return f.listedNames, f.listedFresh, f.listedErr
}

func (f *fakeStorage) AddNewRelease(_ context.Context, e *storageModel.CheckLogEntry, firstCheck time.Time) (bool, error) {
	if f.newAddErr != nil {
		return false, f.newAddErr
	}
	if _, ok := f.knownHashes[e.URLHash]; ok {
		return false, nil
	}
	for _, n := range f.knownNames {
		if storageModel.FoldName(n) == storageModel.FoldName(e.ItemName) {
			return false, nil
		}
	}
	f.newAdds = append(f.newAdds, newAdd{entry: *e, at: firstCheck})
	if f.knownHashes == nil {
		f.knownHashes = map[string]struct{}{}
	}
	f.knownHashes[e.URLHash] = struct{}{}
	f.knownNames = append(f.knownNames, e.ItemName)
	return true, nil
}

func (f *fakeStorage) PurgeDuplicateNames(_ context.Context, live map[string][]string) error {
	f.purges = append(f.purges, live)
	return nil
}

func (f *fakeStorage) SetForceCheck(_ context.Context, urlHash string, force bool) (bool, error) {
	if f.items == nil {
		return false, nil
	}
	for _, it := range f.items {
		if it.URLHash == urlHash {
			it.ForceCheck = force
			return true, nil
		}
	}
	return false, nil
}

type fakeManager struct {
	tagged          []*model.Entry
	all             []*model.Entry
	allErr          error
	partialList     bool
	getEntriesCalls int
	attached        []string
}

func (f *fakeManager) GetEntries(_ context.Context, _ model.FilterOptions) ([]*model.Entry, error) {
	f.getEntriesCalls++
	if f.allErr != nil {
		return nil, f.allErr
	}
	entries := f.all
	if len(entries) == 0 {
		entries = f.tagged
	}
	if f.partialList && len(entries) > 0 {
		return entries, fmt.Errorf("%w: test", model.ErrPartialList)
	}
	return entries, nil
}

func (f *fakeManager) Submit(context.Context, *bytes.Buffer, model.ManagerAddFileOptions) error {
	return nil
}

func (f *fakeManager) SubmitMagnet(context.Context, string, model.ManagerAddFileOptions) error {
	return nil
}

type fakeTracker struct {
	id int
}

func (f *fakeTracker) BuildCache(context.Context) error { return nil }
func (f *fakeTracker) CachedList() map[int]client.ITracker {
	return map[int]client.ITracker{f.id: nil}
}
func (f *fakeTracker) GetID(string) int     { return f.id }
func (f *fakeTracker) GetHourLimit(int) int { return 100 }
func (f *fakeTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	return nil, nil
}
func (f *fakeTracker) Response(context.Context, int, string, bool) (*bytes.Buffer, error) {
	return nil, nil
}
func (f *fakeTracker) Adopt(context.Context, string, string) (string, error) { return "", nil }

func TestEntryMatchesWatchTags(t *testing.T) {
	cases := []struct {
		entry string
		watch []string
		want  bool
	}{
		{"watch", []string{"watch"}, true},
		{"watch,other", []string{"other"}, true},
		{" watch , other ", []string{"watch"}, true},
		{"other", []string{"watch"}, false},
	}
	for _, c := range cases {
		if got := entryMatchesWatchTags(c.entry, c.watch); got != c.want {
			t.Errorf("entryMatchesWatchTags(%q, %v) = %v, want %v", c.entry, c.watch, got, c.want)
		}
	}
}

func TestPushFiltersByWatchTags(t *testing.T) {
	const matched = "https://example.com/t/1"
	const other = "https://example.com/t/2"
	h := &handler{
		config: model.HandlerConfig{WatchTags: []string{"watch", "track"}},
		manager: &fakeManager{all: []*model.Entry{
			{URL: matched, Name: "matched", Tag: "watch"},
			{URL: other, Name: "other", Tag: "skip"},
		}},
		storage:        &fakeStorage{},
		trackerService: &fakeTracker{id: 1},
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case tk := <-h.idx[1]:
		if tk.entry.URL != matched {
			t.Fatalf("queued %q, want %q", tk.entry.URL, matched)
		}
	default:
		t.Fatal("expected matched entry to be queued")
	}
	select {
	case <-h.idx[1]:
		t.Fatal("unexpected second entry queued")
	default:
	}
}

func TestPushForcedQueuesOnlyForcedEntriesWithPriority(t *testing.T) {
	const forcedURL = "https://example.com/t/1"
	const otherURL = "https://example.com/t/2"
	h := &handler{
		manager: &fakeManager{all: []*model.Entry{
			{URL: forcedURL, Name: "forced"},
			{URL: otherURL, Name: "other"},
		}},
		storage:        &fakeStorage{},
		trackerService: &fakeTracker{id: 1},
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	forced := map[string]struct{}{storagePkg.URLHash(forcedURL): {}}
	if err := h.push(ctx, false, forced); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.idx[1]:
		t.Fatal("forced pass must not use the normal queue")
	default:
	}
	select {
	case tk := <-h.pri[1]:
		if tk.entry.URL != forcedURL {
			t.Fatalf("queued %q, want %q", tk.entry.URL, forcedURL)
		}
	default:
		t.Fatal("expected forced entry in the priority queue")
	}
	select {
	case <-h.pri[1]:
		t.Fatal("unexpected second entry queued")
	default:
	}
}

func TestNextTaskPrefersPriorityQueue(t *testing.T) {
	h := &handler{trackerService: &fakeTracker{id: 1}}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	normal := &task{entry: &model.Entry{Name: "normal"}}
	forced := &task{entry: &model.Entry{Name: "forced"}}
	h.idx[1] <- normal
	h.pri[1] <- forced
	if tk := h.nextTask(ctx, 1); tk != forced {
		t.Fatalf("got %v, want the forced task first", tk.entry.Name)
	}
	if tk := h.nextTask(ctx, 1); tk != normal {
		t.Fatalf("got %v, want the normal task second", tk.entry.Name)
	}
}

func TestPushEmptyWatchTagsIncludesAll(t *testing.T) {
	const first = "https://example.com/t/1"
	const second = "https://example.com/t/2"
	h := &handler{
		config: model.HandlerConfig{WatchTags: nil},
		manager: &fakeManager{all: []*model.Entry{
			{URL: first, Name: "a", Tag: "watch"},
			{URL: second, Name: "b", Tag: "other"},
		}},
		storage:        &fakeStorage{},
		trackerService: &fakeTracker{id: 1},
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-h.idx[1]
		<-h.idx[1]
	}()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPushSkipsIgnored(t *testing.T) {
	const url = "https://rutracker.org/forum/viewtopic.php?t=99"
	hash := storagePkg.URLHash(url)
	h := &handler{
		config:         model.HandlerConfig{WatchTags: []string{"watch"}},
		manager:        &fakeManager{all: []*model.Entry{{URL: url, Name: "x", Tag: "watch"}}},
		storage:        &fakeStorage{ignore: map[string]struct{}{hash: {}}},
		trackerService: &fakeTracker{id: 1},
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.idx[1]:
		t.Fatal("ignored entry should not be queued")
	default:
	}
}

func TestPushForcedBypassesIgnore(t *testing.T) {
	const url = "https://rutracker.org/forum/viewtopic.php?t=99"
	hash := storagePkg.URLHash(url)
	newHandler := func() *handler {
		return &handler{
			config:         model.HandlerConfig{WatchTags: []string{"watch"}},
			manager:        &fakeManager{all: []*model.Entry{{URL: url, Name: "x", Tag: "watch"}}},
			storage:        &fakeStorage{ignore: map[string]struct{}{hash: {}}},
			trackerService: &fakeTracker{id: 1},
		}
	}

	forced := newHandler()
	forced.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := forced.push(ctx, false, map[string]struct{}{hash: {}}); err != nil {
		t.Fatal(err)
	}
	select {
	case tk := <-forced.pri[1]:
		if tk.entry.URL != url {
			t.Fatalf("queued %q, want %q", tk.entry.URL, url)
		}
	default:
		t.Fatal("expected forced pass to reach the ignored entry")
	}

	normal := newHandler()
	normal.initTrackerChannels()
	if err := normal.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-normal.idx[1]:
		t.Fatal("normal pass must still skip the ignored entry")
	default:
	}
}

func TestPushDedupesSameName(t *testing.T) {
	early := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	late := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	const earlyURL = "https://example.com/t/early"
	const lateURL = "https://example.com/t/late"
	h := &handler{
		config: model.HandlerConfig{WatchTags: []string{"watch"}},
		manager: &fakeManager{all: []*model.Entry{
			{URL: lateURL, Name: "Same Release", Tag: "watch", Timestamp: &late},
			{URL: earlyURL, Name: "Same Release", Tag: "watch", Timestamp: &early},
		}},
		storage:        &fakeStorage{},
		trackerService: &fakeTracker{id: 1},
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case tk := <-h.idx[1]:
		if tk.entry.URL != earlyURL {
			t.Fatalf("queued %q, want earliest %q", tk.entry.URL, earlyURL)
		}
	default:
		t.Fatal("expected entry to be queued")
	}
	select {
	case <-h.idx[1]:
		t.Fatal("duplicate name should not be queued twice")
	default:
	}
}

func TestNextRun(t *testing.T) {
	loc := time.FixedZone("test", 0)
	h := &handler{config: model.HandlerConfig{
		ListRefreshPerDay: 2,
		StartHour:         7,
		Location:          loc,
	}}
	cases := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"before anchor", time.Date(2025, 1, 15, 5, 0, 0, 0, loc), time.Date(2025, 1, 15, 7, 0, 0, 0, loc)},
		{"exactly at anchor", time.Date(2025, 1, 15, 7, 0, 0, 0, loc), time.Date(2025, 1, 15, 19, 0, 0, 0, loc)},
		{"after morning slot", time.Date(2025, 1, 15, 8, 30, 0, 0, loc), time.Date(2025, 1, 15, 19, 0, 0, 0, loc)},
		{"late evening", time.Date(2025, 1, 15, 23, 0, 0, 0, loc), time.Date(2025, 1, 16, 7, 0, 0, 0, loc)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := h.nextRun(c.now); !got.Equal(c.want) {
				t.Errorf("nextRun(%v) = %v, want %v", c.now, got, c.want)
			}
		})
	}

	h1 := &handler{config: model.HandlerConfig{
		ListRefreshPerDay: 1,
		StartHour:         7,
		Location:          loc,
	}}
	now := time.Date(2025, 1, 15, 10, 0, 0, 0, loc)
	want := time.Date(2025, 1, 16, 7, 0, 0, 0, loc)
	if got := h1.nextRun(now); !got.Equal(want) {
		t.Errorf("nextRun(%v) with 1/day = %v, want %v", now, got, want)
	}
}

func TestNextRunGrid(t *testing.T) {
	loc := time.FixedZone("test", 0)
	at := func(day, h, m int) time.Time { return time.Date(2025, 1, day, h, m, 0, 0, loc) }
	cases := []struct {
		name          string
		perDay, start int
		now, want     time.Time
	}{
		{"8/day before start hour", 8, 8, at(15, 2, 0), at(15, 5, 0)},
		{"8/day at 05:00", 8, 8, at(15, 5, 0), at(15, 8, 0)},
		{"8/day late evening", 8, 8, at(15, 23, 30), at(16, 2, 0)},
		{"perDay 0 as 1/day", 0, 7, at(15, 10, 0), at(16, 7, 0)},
		{"perDay 25 as 1/day", 25, 7, at(15, 10, 0), at(16, 7, 0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &handler{config: model.HandlerConfig{ListRefreshPerDay: c.perDay, StartHour: c.start, Location: loc}}
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("nextRun(%v) panicked: %v", c.now, r)
				}
			}()
			if got := h.nextRun(c.now); !got.Equal(c.want) {
				t.Errorf("nextRun(%v) = %v, want %v", c.now, got, c.want)
			}
		})
	}
}

func TestGetRecheckAt(t *testing.T) {
	h := &handler{config: model.HandlerConfig{
		ThresholdMonth:    1,
		ThresholdHalfYear: 7,
		ThresholdYear:     30,
		ThresholdOld:      90,
	}}

	msk := time.FixedZone("MSK", 3*60*60)
	slot := time.Date(2026, 3, 15, 22, 0, 0, 0, msk)
	now := time.Date(2026, 3, 15, 22, 6, 0, 0, msk)
	cases := []struct {
		name     string
		tm       *time.Time
		wantDays int
	}{
		{"nil timestamp", nil, 1},
		{"zero-value year", timePtr(time.Time{}), 1},
		{"two hours old", timePtr(now.Add(-2 * time.Hour)), 1},
		{"a couple months old", timePtr(now.Add(-60 * 24 * time.Hour)), 7},
		{"under a year old", timePtr(now.Add(-200 * 24 * time.Hour)), 30},
		{"over a year old", timePtr(now.Add(-400 * 24 * time.Hour)), 90},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := h.getRecheckAt(c.tm, slot, now, false, nil)
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			want := time.Date(2026, 3, 15+c.wantDays, 22, 0, 0, 0, msk)
			if !got.Equal(want) {
				t.Errorf("getRecheckAt(%v) = %v, want %v (+%d days)", c.tm, got, want, c.wantDays)
			}
		})
	}
}

func TestWorkerUsesEntryPassSlot(t *testing.T) {
	msk := time.FixedZone("MSK", 3*60*60)
	d := time.Now().Add(-2 * time.Hour)
	h, store, _ := newPickHandler(pickPageClient{pick: tracker.Pick{Date: &d}})
	h.config.ListRefreshPerDay = 12
	h.config.Location = msk
	e := &model.Entry{
		URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", Timestamp: &d, Update: true,
		PassSlot: time.Date(2026, 3, 15, 22, 0, 0, 0, msk),
	}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 1 || store.saved[0].RecheckAt == nil {
		t.Fatalf("saved = %+v, want one item with RecheckAt", store.saved)
	}
	want := time.Date(2026, 3, 16, 22, 0, 0, 0, msk)
	if got := *store.saved[0].RecheckAt; !got.Equal(want) {
		t.Fatalf("RecheckAt = %v, want %v", got, want)
	}
}

func TestPushRecheckOnPassSlot(t *testing.T) {
	const url = "https://pick.example/t/1"
	urlHash := storagePkg.URLHash(url)
	date := time.Now().Add(-2 * time.Hour)
	h, store, _ := newPickHandler(pickPageClient{pick: tracker.Pick{Date: &date}})
	h.config = model.HandlerConfig{
		ListRefreshPerDay: 24, StartHour: 0, Location: time.UTC,
		ThresholdMonth: 1, ThresholdHalfYear: 7, ThresholdYear: 30, ThresholdOld: 30,
	}
	store.items = map[string]*runnerModel.HistoryItem{urlHash: {
		URLHash: urlHash, URL: url, TrackerTypeID: 1, Exists: true, CreatedAt: &date,
	}}
	h.manager = &fakeManager{all: []*model.Entry{{URL: url, Name: "test"}}}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before := time.Now()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	var tk *task
	select {
	case tk = <-h.idx[1]:
	default:
		t.Fatal("entry was not queued")
	}
	if err := h.worker(ctx, 1, tk.entry, nil); err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	if len(store.saved) != 1 || store.saved[0].RecheckAt == nil {
		t.Fatalf("saved = %+v, want one item with RecheckAt", store.saved)
	}
	got := *store.saved[0].RecheckAt
	want1 := before.UTC().Truncate(time.Hour).Add(24 * time.Hour)
	want2 := after.UTC().Truncate(time.Hour).Add(24 * time.Hour)
	if !got.Equal(want1) && !got.Equal(want2) {
		t.Fatalf("RecheckAt = %v, want %v (pass slot + 1 day)", got, want1)
	}
}

func TestPushSkipsHistoryPurgeOnPartialList(t *testing.T) {
	const gone = "https://rutracker.org/forum/viewtopic.php?t=1"
	const alive = "https://rutracker.org/forum/viewtopic.php?t=2"
	goneHash := storagePkg.URLHash(gone)
	hist := map[string]*runnerModel.HistoryItem{goneHash: {URLHash: goneHash, Name: "gone"}}
	store := &fakeStorage{items: hist}
	h := &handler{
		manager: &fakeManager{
			all:         []*model.Entry{{URL: alive, Name: "alive"}},
			partialList: true,
		},
		storage:        store,
		trackerService: &fakeTracker{id: 1},
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("expected no deletions on partial list, got %v", store.deleted)
	}
}

func TestPushContinuesOnPartialList(t *testing.T) {
	const matched = "https://example.com/t/1"
	h := &handler{
		config: model.HandlerConfig{WatchTags: []string{"watch"}},
		manager: &fakeManager{
			all:         []*model.Entry{{URL: matched, Name: "matched", Tag: "watch"}},
			partialList: true,
		},
		storage:        &fakeStorage{},
		trackerService: &fakeTracker{id: 1},
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatalf("push err = %v, want nil", err)
	}
	select {
	case tk := <-h.idx[1]:
		if tk.entry.URL != matched {
			t.Fatalf("queued %q, want %q", tk.entry.URL, matched)
		}
	default:
		t.Fatal("expected matched entry to be queued")
	}
}

func TestPurgeStaleHistoryDeletesAbsent(t *testing.T) {
	const gone = "https://rutracker.org/forum/viewtopic.php?t=1"
	const alive = "https://rutracker.org/forum/viewtopic.php?t=2"
	goneHash := storagePkg.URLHash(gone)
	hist := map[string]*runnerModel.HistoryItem{goneHash: {URLHash: goneHash, Name: "gone"}}
	store := &fakeStorage{items: hist}
	h := &handler{storage: store}
	h.purgeStaleHistory(context.Background(), []*model.Entry{{URL: alive}}, hist)
	if len(store.deleted) != 1 || store.deleted[0] != goneHash {
		t.Fatalf("deleted = %v, want [%s]", store.deleted, goneHash)
	}
}

func TestPurgeStaleHistoryKeepsPresent(t *testing.T) {
	const url = "https://rutracker.org/forum/viewtopic.php?t=2"
	hash := storagePkg.URLHash(url)
	hist := map[string]*runnerModel.HistoryItem{hash: {URLHash: hash, Name: "alive"}}
	store := &fakeStorage{items: hist}
	h := &handler{storage: store}
	h.purgeStaleHistory(context.Background(), []*model.Entry{{URL: url}}, hist)
	if len(store.deleted) != 0 {
		t.Fatalf("expected no deletions, got %v", store.deleted)
	}
}

func TestPurgeStaleHistorySkipsOnEmptyTorrentList(t *testing.T) {
	hash := storagePkg.URLHash("https://example.com/topic")
	hist := map[string]*runnerModel.HistoryItem{hash: {URLHash: hash, Name: "x"}}
	store := &fakeStorage{items: hist}
	h := &handler{storage: store}
	h.purgeStaleHistory(context.Background(), nil, hist)
	if len(store.deleted) != 0 {
		t.Fatalf("expected no deletions on empty torrent list, got %v", store.deleted)
	}
}

func TestPurgeStaleHistorySkipsOnNoTorrentURLs(t *testing.T) {
	hash := storagePkg.URLHash("https://example.com/topic")
	hist := map[string]*runnerModel.HistoryItem{hash: {URLHash: hash, Name: "x"}}
	store := &fakeStorage{items: hist}
	h := &handler{storage: store}
	h.purgeStaleHistory(context.Background(), []*model.Entry{{Name: "no comment"}}, hist)
	if len(store.deleted) != 0 {
		t.Fatalf("expected no deletions when no torrent URLs found, got %v", store.deleted)
	}
}

func TestPushMarksRunning(t *testing.T) {
	state := NewState()
	h := &handler{
		config:         model.HandlerConfig{WatchTags: []string{"watch"}},
		manager:        &fakeManager{all: []*model.Entry{{URL: "https://example.com/t/1", Name: "x", Tag: "watch"}}},
		storage:        &fakeStorage{},
		trackerService: &fakeTracker{id: 1},
		state:          state,
	}
	h.initTrackerChannels()
	h.gen = state.Reset()
	go func() {
		<-h.idx[1]
		state.End(h.gen)
	}()
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if state.Running() {
		t.Fatal("expected not running after push completes and worker drained")
	}
}

func TestTriggerStartsPush(t *testing.T) {
	state := NewState()
	pushDone := make(chan struct{}, 1)
	mgr := &countingManager{
		fakeManager: fakeManager{all: []*model.Entry{{URL: "https://example.com/t/1", Name: "x", Tag: "watch"}}},
		pushDone:    pushDone,
	}
	h := &handler{
		config: model.HandlerConfig{
			WatchTags:         []string{"watch"},
			ListRefreshPerDay: 1,
			StartHour:         23,
			ThresholdMonth:    1,
			ThresholdHalfYear: 1,
			ThresholdYear:     1,
			ThresholdOld:      1,
			Immediate:         false,
		},
		manager:        mgr,
		storage:        &fakeStorage{},
		trackerService: &safeFakeTracker{id: 1},
		state:          state,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)
	time.Sleep(50 * time.Millisecond)
	state.RequestRun()
	select {
	case <-pushDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for push via trigger")
	}
}

type countingManager struct {
	fakeManager
	pushDone chan struct{}
}

func (c *countingManager) GetEntries(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error) {
	select {
	case c.pushDone <- struct{}{}:
	default:
	}
	return c.fakeManager.GetEntries(ctx, opts)
}

type safeFakeTracker struct {
	id int
}

func (f *safeFakeTracker) BuildCache(context.Context) error { return nil }
func (f *safeFakeTracker) CachedList() map[int]client.ITracker {
	return map[int]client.ITracker{f.id: nil}
}
func (f *safeFakeTracker) GetID(string) int     { return f.id }
func (f *safeFakeTracker) GetHourLimit(int) int { return 100 }
func (f *safeFakeTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	return nil, fmt.Errorf("test skip")
}
func (f *safeFakeTracker) Response(context.Context, int, string, bool) (*bytes.Buffer, error) {
	return nil, nil
}
func (f *safeFakeTracker) Adopt(context.Context, string, string) (string, error) {
	return "", nil
}

func TestRunConsumeRestartStartsPush(t *testing.T) {
	state := NewState()
	state.MarkRestart()
	state.RequestRun()
	select {
	case <-state.Trigger():
	default:
		t.Fatal("simulate old runner eating trigger")
	}
	pushDone := make(chan struct{}, 1)
	mgr := &countingManager{
		fakeManager: fakeManager{all: []*model.Entry{{URL: "https://example.com/t/1", Name: "x", Tag: "watch"}}},
		pushDone:    pushDone,
	}
	h := &handler{
		config: model.HandlerConfig{
			WatchTags:         []string{"watch"},
			ListRefreshPerDay: 1,
			StartHour:         23,
			ThresholdMonth:    1,
			ThresholdHalfYear: 1,
			ThresholdYear:     1,
			ThresholdOld:      1,
			Immediate:         false,
		},
		manager:        mgr,
		storage:        &fakeStorage{},
		trackerService: &safeFakeTracker{id: 1},
		state:          state,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)
	select {
	case <-pushDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for push via restart flag")
	}
}

func TestZombiePushDoesNotAffectRunning(t *testing.T) {
	state := NewState()
	oldGen := state.Reset()
	state.Begin(oldGen)
	state.Begin(oldGen)
	if !state.Running() {
		t.Fatal("setup: expected running")
	}
	state.Reset()
	if state.Running() {
		t.Fatal("expected not running after Reset")
	}
	state.Begin(oldGen)
	state.End(oldGen)
	if state.Running() {
		t.Fatal("stale zombie Begin/End must not affect Running")
	}
}

func TestRunResetsStuckCounter(t *testing.T) {
	state := NewState()
	staleGen := state.Reset()
	state.Begin(staleGen)
	state.Begin(staleGen)
	if !state.Running() {
		t.Fatal("setup: expected stuck running")
	}
	h := &handler{
		config: model.HandlerConfig{
			ListRefreshPerDay: 1,
			StartHour:         23,
			ThresholdMonth:    1,
			ThresholdHalfYear: 1,
			ThresholdYear:     1,
			ThresholdOld:      1,
			Immediate:         false,
		},
		trackerService: &safeFakeTracker{id: 1},
		state:          state,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)
	deadline := time.Now().Add(500 * time.Millisecond)
	for state.Running() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if state.Running() {
		t.Fatal("expected counter reset at Run start")
	}
}

type loggingStorage struct {
	fakeStorage
	logs  []storageModel.CheckLogEntry
	saved []runnerModel.HistoryItem
}

func (l *loggingStorage) AddCheckLog(_ context.Context, e *storageModel.CheckLogEntry) (int, error) {
	l.logs = append(l.logs, *e)
	return l.checkStreak, nil
}

func (l *loggingStorage) SaveItem(_ context.Context, item *runnerModel.HistoryItem) error {
	l.saved = append(l.saved, *item)
	return nil
}

type mockPageClient struct{}

func (mockPageClient) GetName() string                            { return "mock" }
func (mockPageClient) GetDomain() string                          { return "mock" }
func (mockPageClient) GetHost() string                            { return "mock" }
func (mockPageClient) GetProxyType() proxyModel.ProxyType         { return proxyModel.TypeNone }
func (mockPageClient) GetPublicity() string                       { return "" }
func (mockPageClient) GetHourLimit() int                          { return 1 }
func (mockPageClient) GetCookies() []*http.Cookie                 { return nil }
func (mockPageClient) GetCredentials() string                     { return "" }
func (mockPageClient) GetMirrors() []string                       { return nil }
func (mockPageClient) LoginLink() string                          { return "" }
func (mockPageClient) DownloadLink(string) string                 { return "" }
func (mockPageClient) TopicLink(string) string                    { return "" }
func (mockPageClient) Detect(string) bool                         { return true }
func (mockPageClient) Headers() *http.Header                      { return &http.Header{} }
func (mockPageClient) ParseDate(*bytes.Buffer, bool) *time.Time   { return nil }
func (mockPageClient) ParseDownloadID(*bytes.Buffer, bool) string { return "" }
func (mockPageClient) ParsePoster(*bytes.Buffer, bool) string     { return "" }
func (mockPageClient) ParseMagnet(*bytes.Buffer, bool) string     { return "" }
func (mockPageClient) PickTorrent(*bytes.Buffer, bool, tracker.Held) tracker.Pick {
	return tracker.Pick{}
}
func (mockPageClient) AdoptLookup(string, string) string                        { return "" }
func (mockPageClient) AdoptURL(*bytes.Buffer) string                            { return "" }
func (mockPageClient) PageLink(link string) string                              { return link }
func (mockPageClient) Cookies() []*http.Cookie                                  { return nil }
func (mockPageClient) InitClient(context.Context, *tracker.Tracker, bool) error { return nil }
func (mockPageClient) GetResponse(context.Context, string, bool) (*http.Response, error) {
	return nil, nil
}

type pageTracker struct {
	safeFakeTracker
	page *bytes.Buffer
}

func (p *pageTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	return mockPageClient{}, nil
}

func (p *pageTracker) Response(context.Context, int, string, bool) (*bytes.Buffer, error) {
	return p.page, nil
}

func TestWorkerUnknownStatusWhenDateMissing(t *testing.T) {
	page := bytes.NewBufferString(strings.Repeat("x", 60))
	store := &loggingStorage{}
	h := &handler{
		config:         model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage:        store,
		trackerService: &pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: page},
		mx:             helper.KeyedMutex{},
	}
	e := &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc"}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(store.logs))
	}
	if store.logs[0].Status != "unknown" {
		t.Fatalf("status = %q, want unknown", store.logs[0].Status)
	}
	wantSample := strings.Repeat("x", 60)
	if store.logs[0].Error != wantSample {
		t.Fatalf("error = %q, want page sample %q", store.logs[0].Error, wantSample)
	}
}

type canceledTracker struct {
	safeFakeTracker
}

func (c *canceledTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	return nil, context.Canceled
}

func TestTrackerWorkerRecoversFromPanic(t *testing.T) {
	page := bytes.NewBufferString(strings.Repeat("x", 60))
	store := &loggingStorage{}
	state := NewState()
	gen := state.Reset()
	h := &handler{
		config:         model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage:        store,
		trackerService: &panicPageTracker{pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: page}},
		state:          state,
		mx:             helper.KeyedMutex{},
		gen:            gen,
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cherr := make(chan error, 1)
	go h.trackerWorker(ctx, 1, cherr)

	h.state.Begin(h.gen)
	e := &model.Entry{URL: "https://example.com/t/1", Name: "panic-test", URLHash: "abc", Timestamp: timePtr(time.Now().Add(-time.Hour))}
	h.idx[1] <- &task{entry: e}

	deadline := time.Now().Add(2 * time.Second)
	for state.Running() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if state.Running() {
		t.Fatal("expected active counter back to zero")
	}
	if len(store.logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(store.logs))
	}
	if store.logs[0].Status != "error" {
		t.Fatalf("status = %q, want error", store.logs[0].Status)
	}
	if !strings.Contains(store.logs[0].Error, "panic:") {
		t.Fatalf("error = %q, want panic message", store.logs[0].Error)
	}
}

type panicClient struct {
	mockPageClient
}

func (panicClient) ParseDate(*bytes.Buffer, bool) *time.Time {
	panic("parse date boom")
}

type panicPageTracker struct {
	pageTracker
}

func (p *panicPageTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	return panicClient{}, nil
}

func TestWorkerSkipsCheckLogOnContextCanceled(t *testing.T) {
	store := &loggingStorage{}
	h := &handler{
		storage:        store,
		trackerService: &canceledTracker{safeFakeTracker: safeFakeTracker{id: 1}},
		mx:             helper.KeyedMutex{},
	}
	e := &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc"}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatalf("worker err = %v, want nil", err)
	}
	if len(store.logs) != 0 {
		t.Fatalf("expected no check_log entries, got %d", len(store.logs))
	}
}

func TestGetHistorySkipsFutureRecheckAt(t *testing.T) {
	const url = "https://rutracker.org/forum/viewtopic.php?t=50"
	store := &fakeStorage{
		items: map[string]*runnerModel.HistoryItem{
			url: {URLHash: storagePkg.URLHash(url), TrackerTypeID: 1, Exists: true, RecheckAt: timePtr(time.Now().Add(24 * time.Hour))},
		},
	}
	h := &handler{storage: store}
	items, err := store.AllItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hi, err := h.getHistory(context.Background(), url, false, items)
	if err != errSkipped {
		t.Fatalf("err = %v, want errSkipped", err)
	}
	if hi == nil || !hi.Exists {
		t.Fatal("expected history item")
	}
}

func TestGetHistoryForceCheckBypassesRecheckAt(t *testing.T) {
	const url = "https://rutracker.org/forum/viewtopic.php?t=51"
	store := &fakeStorage{
		items: map[string]*runnerModel.HistoryItem{
			url: {URLHash: storagePkg.URLHash(url), TrackerTypeID: 1, Exists: true, RecheckAt: timePtr(time.Now().Add(24 * time.Hour)), ForceCheck: true},
		},
	}
	h := &handler{storage: store}
	items, err := store.AllItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hi, err := h.getHistory(context.Background(), url, false, items)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if hi == nil || !hi.ForceCheck {
		t.Fatal("expected force_check item")
	}
}

func TestGetHistoryReturnsDueItemFromMap(t *testing.T) {
	const url = "https://rutracker.org/forum/viewtopic.php?t=60"
	store := &fakeStorage{
		items: map[string]*runnerModel.HistoryItem{
			url: {URLHash: storagePkg.URLHash(url), TrackerTypeID: 1, Exists: true, RecheckAt: timePtr(time.Now().Add(-time.Hour))},
		},
	}
	h := &handler{storage: store}
	items, err := store.AllItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hi, err := h.getHistory(context.Background(), url, false, items)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if hi == nil || !hi.Exists {
		t.Fatal("expected history item from map")
	}
}

type mirrorPageClient struct {
	mockPageClient
	primary string
	mirror  string
}

func (m *mirrorPageClient) GetDomain() string     { return m.mirror }
func (m *mirrorPageClient) PrimaryDomain() string { return m.primary }

func TestEntryTopicURLUsesPrimaryDomain(t *testing.T) {
	tc := &mirrorPageClient{primary: "rutracker.org", mirror: "mirror.example"}
	e := &model.Entry{URL: "https://mirror.example/forum/viewtopic.php?t=1"}
	got := entryTopicURL(tc, e)
	want := "https://rutracker.org/forum/viewtopic.php?t=1"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

type updatingPageClient struct {
	mockPageClient
}

func (updatingPageClient) ParseDate(*bytes.Buffer, bool) *time.Time {
	t := time.Now()
	return &t
}

func (updatingPageClient) ParseMagnet(*bytes.Buffer, bool) string {
	return "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
}

type updatingPageTracker struct {
	pageTracker
}

func (p *updatingPageTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	return updatingPageClient{}, nil
}

type errAlreadyAddedManager struct {
	fakeManager
}

func (m *errAlreadyAddedManager) Submit(context.Context, *bytes.Buffer, model.ManagerAddFileOptions) error {
	// submit SubmitMagnet уже не зовет, а h.magnetFetch тут не задан,
	// так что запись уходит файлом через Submit
	return fmt.Errorf("%w: test", model.ErrAlreadyAdded)
}

func TestWorkerUnchangedOnAlreadyAdded(t *testing.T) {
	page := bytes.NewBufferString(strings.Repeat("x", 60))
	store := &loggingStorage{}
	old := time.Now().Add(-48 * time.Hour)
	h := &handler{
		config:  model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage: store,
		manager: &errAlreadyAddedManager{},
		trackerService: &updatingPageTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: page},
		},
		mx: helper.KeyedMutex{},
	}
	e := &model.Entry{
		URL:       "https://example.com/t/1",
		Name:      "test",
		URLHash:   "abc",
		Timestamp: &old,
	}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(store.logs))
	}
	if store.logs[0].Status != "unchanged" {
		t.Fatalf("status = %q, want unchanged", store.logs[0].Status)
	}
	if store.logs[0].Error != "" {
		t.Fatalf("error = %q, want empty", store.logs[0].Error)
	}
}

type errHashUnchangedManager struct {
	fakeManager
}

func (m *errHashUnchangedManager) Submit(context.Context, *bytes.Buffer, model.ManagerAddFileOptions) error {
	return fmt.Errorf("%w: test", model.ErrHashUnchanged)
}

func TestWorkerSavesHistoryOnHashUnchangedWithoutErrorStreak(t *testing.T) {
	// ErrHashUnchanged из Submit это нормальный исход. История должна сохраниться с новой
	// датой, иначе следующий прогон снова увидит новую дату темы и пойдет по кругу
	page := bytes.NewBufferString(strings.Repeat("x", 60))
	store := &loggingStorage{}
	old := time.Now().Add(-48 * time.Hour)
	h := &handler{
		config:  model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage: store,
		manager: &errHashUnchangedManager{},
		trackerService: &updatingPageTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: page},
		},
		mx: helper.KeyedMutex{},
	}
	e := &model.Entry{
		URL:       "https://example.com/t/1",
		Name:      "test",
		URLHash:   "abc",
		Timestamp: &old,
	}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(store.logs))
	}
	if store.logs[0].Status != "unchanged" {
		t.Fatalf("status = %q, want unchanged: торрент остался на месте", store.logs[0].Status)
	}
	if store.logs[0].Error != "" {
		t.Fatalf("error = %q, want empty (not counted as an error)", store.logs[0].Error)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved history items = %d, want 1 (new date must be recorded)", len(store.saved))
	}
}

type fixedDatePageClient struct {
	mockPageClient
	date time.Time
}

func (f fixedDatePageClient) ParseDate(*bytes.Buffer, bool) *time.Time {
	return &f.date
}

func (f fixedDatePageClient) ParseMagnet(*bytes.Buffer, bool) string {
	return "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
}

type fixedDatePageTracker struct {
	pageTracker
	date time.Time
}

func (p *fixedDatePageTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	return fixedDatePageClient{date: p.date}, nil
}

type submitTrackingManager struct {
	fakeManager
	submitted bool
}

func (m *submitTrackingManager) Submit(context.Context, *bytes.Buffer, model.ManagerAddFileOptions) error {
	m.submitted = true
	return nil
}

type magnetTrackingManager struct {
	fakeManager
	submitCalled bool
	// submit SubmitMagnet больше не зовет, флаг ловит возврат старого пути
	submitMagnetCalled bool
	// по этим байтам проверяем комментарий
	lastSubmitData []byte
}

func (m *magnetTrackingManager) Submit(_ context.Context, buf *bytes.Buffer, _ model.ManagerAddFileOptions) error {
	m.submitCalled = true
	if buf != nil {
		m.lastSubmitData = buf.Bytes()
	}
	return nil
}

func (m *magnetTrackingManager) SubmitMagnet(context.Context, string, model.ManagerAddFileOptions) error {
	m.submitMagnetCalled = true
	return nil
}

func buildTestTorrentBytes(t *testing.T, comment string) []byte {
	t.Helper()
	info := metainfo.Info{PieceLength: 16384, Pieces: make([]byte, 20), Name: "test.txt", Length: 0}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes, Comment: comment}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes()
}

func TestSubmit_MagnetMetadataSuccessUsesFileSubmitWithComment(t *testing.T) {
	mgr := &magnetTrackingManager{}
	h := &handler{
		manager: mgr,
		config:  model.HandlerConfig{MagnetTimeout: 5},
		magnetFetch: func(context.Context, string) ([]byte, error) {
			// комментарий тут сырой, в файл его потом ставит torrentmeta.SetComment
			// внутри buildTorrentFromMagnet
			return buildTestTorrentBytes(t, "raw"), nil
		},
	}
	e := &model.Entry{Name: "test", Magnet: "magnet:?xt=urn:btih:abc", URL: "https://example.com/t/1"}

	if err := h.submit(context.Background(), 1, mockPageClient{}, e, nil); err != nil {
		t.Fatalf("submit err = %v", err)
	}
	if mgr.submitMagnetCalled {
		t.Error("SubmitMagnet must never be called from submit anymore")
	}
	if !mgr.submitCalled {
		t.Fatal("expected Submit to be called with the built torrent bytes")
	}
	mi, err := metainfo.Load(bytes.NewReader(mgr.lastSubmitData))
	if err != nil {
		t.Fatalf("submitted data is not a valid torrent: %v", err)
	}
	if mi.Comment == "" || mi.Comment == "raw" {
		t.Errorf("comment = %q, want the tracker topic comment stamped by submit", mi.Comment)
	}
}

func TestSubmit_MagnetMetadataFailureFallsBackToTrackerDownload(t *testing.T) {
	page := bytes.NewBufferString(strings.Repeat("x", 60))
	mgr := &magnetTrackingManager{}
	h := &handler{
		manager:        mgr,
		trackerService: &pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: page},
		magnetFetch: func(context.Context, string) ([]byte, error) {
			return nil, fmt.Errorf("swarm unreachable: test")
		},
	}
	e := &model.Entry{Name: "test", Magnet: "magnet:?xt=urn:btih:abc", TopicID: "123"}

	if err := h.submit(context.Background(), 1, mockPageClient{}, e, nil); err != nil {
		t.Fatalf("submit err = %v", err)
	}
	if mgr.submitMagnetCalled {
		t.Error("SubmitMagnet must never be called from submit anymore")
	}
	if !mgr.submitCalled {
		t.Fatal("expected Submit (downloaded .torrent path) to be called instead")
	}
	if !bytes.Equal(mgr.lastSubmitData, page.Bytes()) {
		t.Errorf("expected fallback to submit the bytes downloaded from the tracker page")
	}
}

func runForceCheckWorkerTest(t *testing.T, forceCheck bool) (status string, submitted bool) {
	t.Helper()
	const url = "https://example.com/t/force-check"
	oldAddition := time.Now().Add(-48 * time.Hour)
	historyCreatedAt := time.Now().Add(-2 * time.Hour)
	pageDate := historyCreatedAt

	page := bytes.NewBufferString(strings.Repeat("x", 60))
	store := &loggingStorage{
		fakeStorage: fakeStorage{
			items: map[string]*runnerModel.HistoryItem{
				url: {
					URLHash:       storagePkg.URLHash(url),
					TrackerTypeID: 1,
					TopicID:       "123",
					Exists:        true,
					CreatedAt:     &historyCreatedAt,
					ForceCheck:    forceCheck,
				},
			},
		},
	}
	mgr := &submitTrackingManager{}
	h := &handler{
		config:  model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage: store,
		manager: mgr,
		trackerService: &fixedDatePageTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: page},
			date:        pageDate,
		},
		mx: helper.KeyedMutex{},
	}
	e := &model.Entry{
		URL:       url,
		Name:      "test",
		Timestamp: &oldAddition,
	}
	items, err := store.AllItems(context.Background())
	if err != nil {
		t.Fatalf("AllItems err = %v", err)
	}
	if err := h.buildEntry(context.Background(), e, false, items); err != nil {
		t.Fatalf("buildEntry err = %v", err)
	}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatalf("worker err = %v", err)
	}
	if len(store.logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(store.logs))
	}
	return store.logs[0].Status, mgr.submitted
}

func TestBuildEntryForceCheckKeepsTorrentTimestamp(t *testing.T) {
	const url = "https://example.com/t/force-ts"
	oldAddition := time.Now().Add(-48 * time.Hour)
	historyCreatedAt := time.Now().Add(-2 * time.Hour)
	store := &fakeStorage{
		items: map[string]*runnerModel.HistoryItem{
			url: {
				URLHash:       storagePkg.URLHash(url),
				TrackerTypeID: 1,
				TopicID:       "123",
				Exists:        true,
				CreatedAt:     &historyCreatedAt,
				ForceCheck:    true,
			},
		},
	}
	h := &handler{storage: store}
	e := &model.Entry{URL: url, Timestamp: &oldAddition}
	items, err := store.AllItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.buildEntry(context.Background(), e, false, items); err != nil {
		t.Fatal(err)
	}
	if !e.Update {
		t.Fatal("expected Update=true")
	}
	if e.TopicID != "123" {
		t.Fatalf("TopicID = %q, want 123", e.TopicID)
	}
	if e.Timestamp == nil || !e.Timestamp.Equal(oldAddition) {
		t.Fatalf("Timestamp = %v, want old torrent addition %v", e.Timestamp, oldAddition)
	}
}

func TestWorkerUpdatedOnForceCheck(t *testing.T) {
	status, submitted := runForceCheckWorkerTest(t, true)
	if status != "updated" {
		t.Fatalf("status = %q, want updated", status)
	}
	if !submitted {
		t.Fatal("expected Submit to be called")
	}
}

func TestWorkerUnchangedWithoutForceCheckSameDates(t *testing.T) {
	status, submitted := runForceCheckWorkerTest(t, false)
	if status != "unchanged" {
		t.Fatalf("status = %q, want unchanged", status)
	}
	if submitted {
		t.Fatal("expected Submit not to be called")
	}
}

type forceTrackingStorage struct {
	loggingStorage
	saveCalls int
}

func (f *forceTrackingStorage) SaveItem(_ context.Context, item *runnerModel.HistoryItem) error {
	f.saveCalls++
	for _, it := range f.items {
		if it.URLHash == item.URLHash {
			it.ForceCheck = false
			it.CreatedAt = item.CreatedAt
			it.RecheckAt = item.RecheckAt
		}
	}
	return nil
}

type failingSubmitManager struct {
	submitTrackingManager
}

func (m *failingSubmitManager) Submit(context.Context, *bytes.Buffer, model.ManagerAddFileOptions) error {
	return fmt.Errorf("submit failed")
}

func TestWorkerForceCheckPreservedOnSubmitError(t *testing.T) {
	const url = "https://example.com/t/force-submit-fail"
	oldAddition := time.Now().Add(-48 * time.Hour)
	historyCreatedAt := time.Now().Add(-2 * time.Hour)
	pageDate := historyCreatedAt

	page := bytes.NewBufferString(strings.Repeat("x", 60))
	store := &forceTrackingStorage{
		loggingStorage: loggingStorage{
			fakeStorage: fakeStorage{
				items: map[string]*runnerModel.HistoryItem{
					url: {
						URLHash:       storagePkg.URLHash(url),
						TrackerTypeID: 1,
						TopicID:       "123",
						Exists:        true,
						CreatedAt:     &historyCreatedAt,
						ForceCheck:    true,
					},
				},
			},
		},
	}
	h := &handler{
		config:  model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage: store,
		manager: &failingSubmitManager{},
		trackerService: &fixedDatePageTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: page},
			date:        pageDate,
		},
		mx: helper.KeyedMutex{},
	}
	e := &model.Entry{
		URL:       url,
		Name:      "test",
		Timestamp: &oldAddition,
	}
	items, err := store.AllItems(context.Background())
	if err != nil {
		t.Fatalf("AllItems err = %v", err)
	}
	if err := h.buildEntry(context.Background(), e, false, items); err != nil {
		t.Fatalf("buildEntry err = %v", err)
	}
	if err := h.worker(context.Background(), 1, e, nil); err == nil {
		t.Fatal("expected worker error")
	}
	if store.saveCalls != 0 {
		t.Fatalf("SaveItem calls = %d, want 0", store.saveCalls)
	}
	if !store.items[url].ForceCheck {
		t.Fatal("expected force_check to remain true after submit error")
	}
	if store.logs[0].Status != "error" {
		t.Fatalf("status = %q, want error", store.logs[0].Status)
	}
}

func TestPushDryRunLimitsAndSkipsWrites(t *testing.T) {
	entries := make([]*model.Entry, 35)
	for i := 0; i < 35; i++ {
		entries[i] = &model.Entry{
			URL:  fmt.Sprintf("https://example.com/t/%d", i),
			Name: fmt.Sprintf("Entry %d", i),
			Tag:  "watch",
		}
	}
	h := &handler{
		config:         model.HandlerConfig{WatchTags: []string{"watch"}},
		manager:        &fakeManager{all: entries},
		storage:        &fakeStorage{},
		trackerService: &fakeTracker{id: 1},
		state:          NewState(),
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h.gen = h.state.Reset()
	if err := h.push(ctx, true, nil); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 30; i++ {
		select {
		case tk := <-h.idx[1]:
			if tk.report == nil {
				t.Fatal("expected report to be non-nil in dry run")
			}
		default:
			t.Fatalf("expected 30 tasks, got %d", i)
		}
	}

	select {
	case <-h.idx[1]:
		t.Fatal("expected no more than 30 entries in dry run")
	default:
	}
}

type barrierTracker struct {
	safeFakeTracker
	// с локом на трекер два воркера встретятся на барьере. С одним глобальным локом
	// второй не войдет, и тест упадет по таймауту
	arrive  chan struct{}
	proceed chan struct{}
}

func (b *barrierTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	return mockPageClient{}, nil
}

func (b *barrierTracker) Response(context.Context, int, string, bool) (*bytes.Buffer, error) {
	b.arrive <- struct{}{}
	<-b.proceed
	return bytes.NewBufferString(strings.Repeat("x", 60)), nil
}

func TestWorkerLockIsPerTracker(t *testing.T) {
	tr := &barrierTracker{
		safeFakeTracker: safeFakeTracker{id: 1},
		arrive:          make(chan struct{}, 2),
		proceed:         make(chan struct{}),
	}
	h := &handler{
		config:         model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage:        &fakeStorage{},
		trackerService: tr,
	}

	done := make(chan error, 2)
	for _, ttid := range []int{1, 2} {
		go func(id int) {
			e := &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc"}
			done <- h.worker(context.Background(), id, e, nil)
		}(ttid)
	}

	for i := 0; i < 2; i++ {
		select {
		case <-tr.arrive:
		case <-time.After(5 * time.Second):
			t.Fatal("only one worker reached the tracker: the lock is not per tracker")
		}
	}
	close(tr.proceed)

	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestLogCheckAutoIgnoresOnErrorStreakAboveThreshold(t *testing.T) {
	store := &fakeStorage{checkStreak: 3}
	h := &handler{
		config:  model.HandlerConfig{ErrorStreakThreshold: 3},
		storage: store,
	}
	e := &model.Entry{URL: "https://example.com/t/1", Name: "broken release", URLHash: "abc"}
	h.logCheck(context.Background(), e, 1, "error", "boom", nil)
	if len(store.ignoreCalls) != 1 || store.ignoreCalls[0] != "abc" {
		t.Fatalf("ignoreCalls = %v, want [abc]", store.ignoreCalls)
	}
}

func TestLogCheckDoesNotIgnoreBelowThreshold(t *testing.T) {
	store := &fakeStorage{checkStreak: 2}
	h := &handler{
		config:  model.HandlerConfig{ErrorStreakThreshold: 3},
		storage: store,
	}
	e := &model.Entry{URL: "https://example.com/t/1", Name: "flaky release", URLHash: "abc"}
	h.logCheck(context.Background(), e, 1, "error", "boom", nil)
	if len(store.ignoreCalls) != 0 {
		t.Fatalf("expected no ignore calls below threshold, got %v", store.ignoreCalls)
	}
}

func TestLogCheckAutoIgnoreDisabledWhenThresholdZero(t *testing.T) {
	store := &fakeStorage{checkStreak: 99}
	h := &handler{
		config:  model.HandlerConfig{ErrorStreakThreshold: 0},
		storage: store,
	}
	e := &model.Entry{URL: "https://example.com/t/1", Name: "release", URLHash: "abc"}
	h.logCheck(context.Background(), e, 1, "error", "boom", nil)
	if len(store.ignoreCalls) != 0 {
		t.Fatalf("expected auto-ignore disabled at threshold 0, got %v", store.ignoreCalls)
	}
}

func TestPushCallsGetEntriesOnce(t *testing.T) {
	mgr := &fakeManager{all: []*model.Entry{{URL: "https://example.com/t/1", Name: "x", Tag: "watch"}}}
	h := &handler{
		config:         model.HandlerConfig{WatchTags: []string{"watch"}},
		manager:        mgr,
		storage:        &fakeStorage{},
		trackerService: &fakeTracker{id: 1},
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	if mgr.getEntriesCalls != 1 {
		t.Fatalf("GetEntries calls = %d, want 1", mgr.getEntriesCalls)
	}
}

func TestPurgeStaleHistoryDeletesAbsentNotDueYet(t *testing.T) {
	const gone = "https://rutor.is/torrent/922808"
	const alive = "https://rutracker.org/forum/viewtopic.php?t=2"
	goneHash := storagePkg.URLHash(gone)
	later := time.Now().Add(14 * 24 * time.Hour)
	items := map[string]*runnerModel.HistoryItem{
		goneHash: {URLHash: goneHash, Name: "gone", URL: gone, RecheckAt: &later},
	}
	store := &fakeStorage{items: items}
	h := &handler{storage: store}
	h.purgeStaleHistory(context.Background(), []*model.Entry{{URL: alive}}, items)
	if len(store.deleted) != 1 || store.deleted[0] != goneHash {
		t.Fatalf("deleted = %v, want [%s]", store.deleted, goneHash)
	}
}

func TestPushForcedPurgesHistoryOfDeletedTorrent(t *testing.T) {
	const gone = "https://rutor.is/torrent/922808"
	const alive = "https://rutracker.org/forum/viewtopic.php?t=2"
	goneHash := storagePkg.URLHash(gone)
	hist := map[string]*runnerModel.HistoryItem{goneHash: {URLHash: goneHash, Name: "gone", URL: gone}}
	store := &fakeStorage{items: hist}
	h := &handler{
		manager:        &fakeManager{all: []*model.Entry{{URL: alive, Name: "alive"}}},
		storage:        store,
		trackerService: &fakeTracker{id: 1},
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, map[string]struct{}{goneHash: {}}); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != goneHash {
		t.Fatalf("deleted = %v, want [%s]", store.deleted, goneHash)
	}
}

func TestWorkerSavesHistoryOnAlreadyAdded(t *testing.T) {
	page := bytes.NewBufferString(strings.Repeat("x", 60))
	store := &loggingStorage{}
	old := time.Now().Add(-48 * time.Hour)
	h := &handler{
		config:  model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage: store,
		manager: &errAlreadyAddedManager{},
		trackerService: &updatingPageTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: page},
		},
		mx: helper.KeyedMutex{},
	}
	e := &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc", Timestamp: &old}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	// без записи истории force_check остается взведенным, и каждый прогон снова
	// считает запись обновленной и упирается в already added
	if len(store.saved) != 1 {
		t.Fatalf("saved = %d, want 1", len(store.saved))
	}
	if store.saved[0].RecheckAt == nil || !store.saved[0].RecheckAt.After(time.Now()) {
		t.Fatalf("recheck_at = %v, want a future time", store.saved[0].RecheckAt)
	}
}

func notifyProbe(t *testing.T) (*notify.Notifier, <-chan struct{}) {
	t.Helper()
	// транспорт не важен, смотрим только, ушло ли уведомление
	hits := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		hits <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return notify.New(notify.Config{WebhookURL: srv.URL, Enabled: true}, ""), hits
}

func hashUnchangedHandler(t *testing.T, mgr manager, n *notify.Notifier) (*handler, *model.Entry) {
	t.Helper()
	old := time.Now().Add(-48 * time.Hour)
	h := &handler{
		config:   model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage:  &loggingStorage{},
		manager:  mgr,
		notifier: n,
		trackerService: &updatingPageTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: bytes.NewBufferString(strings.Repeat("x", 60))},
		},
		mx: helper.KeyedMutex{},
	}
	return h, &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc", Timestamp: &old}
}

func TestWorkerHashUnchangedDoesNotNotify(t *testing.T) {
	n, hits := notifyProbe(t)
	h, e := hashUnchangedHandler(t, &errHashUnchangedManager{}, n)
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	// в check_log исход пишется как updated, но перезаливки не было, сообщать не о чем
	select {
	case <-hits:
		t.Fatal("уведомление ушло, хотя торрент не перезаливался")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWorkerRealUpdateNotifies(t *testing.T) {
	// контроль: без него тест выше был бы зеленым и при сломанном транспорте
	n, hits := notifyProbe(t)
	h, e := hashUnchangedHandler(t, &fakeManager{}, n)
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-hits:
	case <-time.After(5 * time.Second):
		t.Fatal("уведомление о перезаливке не ушло")
	}
}

type downloadIDPageClient struct {
	mockPageClient
	date time.Time
}

func (c downloadIDPageClient) ParseDate(*bytes.Buffer, bool) *time.Time { return &c.date }
func (downloadIDPageClient) ParseDownloadID(*bytes.Buffer, bool) string { return "715427" }
func (downloadIDPageClient) DownloadLink(id string) string {
	return "https://nnm.example/download.php?id=" + id
}

type downloadIDTracker struct {
	pageTracker
	date  time.Time
	links []string
	flags []bool
}

func (p *downloadIDTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	return downloadIDPageClient{date: p.date}, nil
}

func (p *downloadIDTracker) Response(_ context.Context, _ int, link string, download bool) (*bytes.Buffer, error) {
	p.links = append(p.links, link)
	p.flags = append(p.flags, download)
	if download {
		return bytes.NewBufferString("torrent"), nil
	}
	return p.page, nil
}

func newDownloadIDHandler(date time.Time) (*handler, *loggingStorage, *downloadIDTracker) {
	store := &loggingStorage{}
	tr := &downloadIDTracker{
		pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: bytes.NewBufferString(strings.Repeat("x", 60))},
		date:        date,
	}
	h := &handler{
		config:         model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage:        store,
		manager:        &fakeManager{},
		trackerService: tr,
		mx:             helper.KeyedMutex{},
	}
	return h, store, tr
}

func TestWorkerFirstSeenDownloadsByPageDownloadID(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	h, store, tr := newDownloadIDHandler(time.Now())
	e := &model.Entry{URL: "https://nnm.example/t/830137", Name: "test", URLHash: "abc", TopicID: "830137", Timestamp: &old}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(tr.links) != 2 || !tr.flags[1] || tr.links[1] != "https://nnm.example/download.php?id=715427" {
		t.Fatalf("links = %v flags = %v, want download by id=715427", tr.links, tr.flags)
	}
	if len(store.saved) != 1 || store.saved[0].TopicID != "715427" {
		t.Fatalf("saved = %+v, want one item with TopicID 715427", store.saved)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "updated" {
		t.Fatalf("logs = %+v, want one updated", store.logs)
	}
}

func TestWorkerFirstSeenUnchangedKeepsPageDownloadID(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	h, store, tr := newDownloadIDHandler(time.Now().Add(-72 * time.Hour))
	e := &model.Entry{URL: "https://nnm.example/t/830137", Name: "test", URLHash: "abc", TopicID: "830137", Timestamp: &old}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(tr.links) != 1 || tr.flags[0] {
		t.Fatalf("links = %v flags = %v, want only the page fetch", tr.links, tr.flags)
	}
	if len(store.saved) != 1 || store.saved[0].TopicID != "715427" {
		t.Fatalf("saved = %+v, want one item with TopicID 715427", store.saved)
	}
}

type failingPageTracker struct {
	pageTracker
	getErr  error
	respErr error
}

func (p *failingPageTracker) GetCachedClient(ctx context.Context, ttid int) (client.IClient, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	return p.pageTracker.GetCachedClient(ctx, ttid)
}

func (p *failingPageTracker) Response(context.Context, int, string, bool) (*bytes.Buffer, error) {
	return nil, p.respErr
}

func runFailingWorker(t *testing.T, tr *failingPageTracker) *loggingStorage {
	t.Helper()
	store := &loggingStorage{fakeStorage: fakeStorage{checkStreak: 5}}
	tr.safeFakeTracker = safeFakeTracker{id: 1}
	h := &handler{
		config:         model.HandlerConfig{ErrorStreakThreshold: 3, ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage:        store,
		trackerService: tr,
		mx:             helper.KeyedMutex{},
	}
	e := &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc"}
	_ = h.worker(context.Background(), 1, e, nil)
	return store
}

func TestWorkerTrackerWideErrorKeepsStreak(t *testing.T) {
	cases := map[string]*failingPageTracker{
		"get cached client": {getErr: errors.New("test skip")},
		"circuit open":      {respErr: fmt.Errorf("mock error: %w", helper.ErrCircuitOpen)},
		"unavailable":       {respErr: fmt.Errorf("%w: login failed", client.ErrTrackerUnavailable)},
	}
	for name, tr := range cases {
		t.Run(name, func(t *testing.T) {
			store := runFailingWorker(t, tr)
			if len(store.logs) != 1 || store.logs[0].Status != "error" || !store.logs[0].KeepStreak {
				t.Fatalf("logs = %+v, want one error with KeepStreak", store.logs)
			}
			if len(store.ignoreCalls) != 0 {
				t.Fatalf("ignoreCalls = %v, want none", store.ignoreCalls)
			}
		})
	}
}

func TestWorkerPerItemErrorCountsStreak(t *testing.T) {
	store := runFailingWorker(t, &failingPageTracker{respErr: errors.New("mock: unexpected status code 404")})
	if len(store.logs) != 1 || store.logs[0].Status != "error" || store.logs[0].KeepStreak {
		t.Fatalf("logs = %+v, want one error without KeepStreak", store.logs)
	}
	if len(store.ignoreCalls) != 1 || store.ignoreCalls[0] != "abc" {
		t.Fatalf("ignoreCalls = %v, want [abc]", store.ignoreCalls)
	}
}

type errReplacePendingManager struct {
	fakeManager
}

func (m *errReplacePendingManager) Submit(context.Context, *bytes.Buffer, model.ManagerAddFileOptions) error {
	return fmt.Errorf("submit error: %w", model.ErrReplacePending)
}

func TestWorkerReplacePendingKeepsStreak(t *testing.T) {
	// незаконченная замена не вина записи: пока пару не разберет подметальщик,
	// пишем error без истории, но в автоигнор не уводим
	store := &loggingStorage{fakeStorage: fakeStorage{checkStreak: 5}}
	old := time.Now().Add(-48 * time.Hour)
	h := &handler{
		config:  model.HandlerConfig{ErrorStreakThreshold: 3, ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage: store,
		manager: &errReplacePendingManager{},
		trackerService: &updatingPageTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: bytes.NewBufferString(strings.Repeat("x", 60))},
		},
		mx: helper.KeyedMutex{},
	}
	e := &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc", Timestamp: &old}
	_ = h.worker(context.Background(), 1, e, nil)
	if len(store.logs) != 1 || store.logs[0].Status != "error" || !store.logs[0].KeepStreak {
		t.Fatalf("logs = %+v, want one error with KeepStreak", store.logs)
	}
	if len(store.ignoreCalls) != 0 || len(store.saved) != 0 {
		t.Fatalf("ignoreCalls = %v, saved = %d, want none", store.ignoreCalls, len(store.saved))
	}
}

type pickPageClient struct {
	mockPageClient
	pick tracker.Pick
	held *tracker.Held
}

func (c pickPageClient) PickTorrent(_ *bytes.Buffer, _ bool, held tracker.Held) tracker.Pick {
	if c.held != nil {
		*c.held = held
	}
	return c.pick
}

func (pickPageClient) DownloadLink(id string) string { return "https://pick.example/dl/" + id }

type pickTracker struct {
	downloadIDTracker
	client pickPageClient
}

func (p *pickTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	return p.client, nil
}

func newPickHandler(c pickPageClient) (*handler, *loggingStorage, *pickTracker) {
	store := &loggingStorage{}
	tr := &pickTracker{
		downloadIDTracker: downloadIDTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: bytes.NewBufferString(strings.Repeat("x", 60))},
		},
		client: c,
	}
	h := &handler{
		config:         model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage:        store,
		manager:        &fakeManager{},
		trackerService: tr,
		mx:             helper.KeyedMutex{},
	}
	return h, store, tr
}

func TestWorkerPickOverridesDownloadIDAndDate(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	d := time.Now().Add(-time.Hour)
	const id = "45155|f|1 GB"
	h, store, tr := newPickHandler(pickPageClient{pick: tracker.Pick{DownloadID: id, Date: &d}})
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", TopicID: id, Timestamp: &old, Update: true}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(tr.links) != 2 || !tr.flags[1] || tr.links[1] != "https://pick.example/dl/"+id {
		t.Fatalf("links = %v flags = %v, want download by %q", tr.links, tr.flags, id)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "updated" {
		t.Fatalf("logs = %+v, want one updated", store.logs)
	}
}

func TestWorkerPickMissIsUnknown(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	h, store, tr := newPickHandler(pickPageClient{pick: tracker.Pick{Miss: "2 torrents"}})
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", TopicID: "1", Timestamp: &old, Update: true}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "unknown" || store.logs[0].Error != "2 torrents" {
		t.Fatalf("logs = %+v, want one unknown with the miss text", store.logs)
	}
	if len(tr.links) != 1 || tr.flags[0] {
		t.Fatalf("links = %v flags = %v, want only the page fetch", tr.links, tr.flags)
	}
	if len(store.saved) != 0 {
		t.Fatalf("saved = %+v, want none", store.saved)
	}
}

func TestWorkerPickGetsHeld(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	var got tracker.Held
	h, _, _ := newPickHandler(pickPageClient{pick: tracker.Pick{Miss: "m"}, held: &got})
	e := &model.Entry{URL: "https://pick.example/t/7", Name: "held name", Hash: "abcdef", URLHash: "abc", TopicID: "7", Timestamp: &old, Update: true}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if got.URL != e.URL || got.TopicID != "7" || !got.Stored || got.Hash != "abcdef" || got.Name != "held name" {
		t.Fatalf("held = %+v", got)
	}
	if got.Timestamp == nil || !got.Timestamp.Equal(old) {
		t.Fatalf("held.Timestamp = %v, want %v", got.Timestamp, old)
	}
}

func TestWorkerPickDateEqualTimestampIsUnchanged(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	same := old
	h, store, tr := newPickHandler(pickPageClient{pick: tracker.Pick{Date: &same}})
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", TopicID: "1", Timestamp: &old, Update: true}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "unchanged" {
		t.Fatalf("logs = %+v, want one unchanged", store.logs)
	}
	if len(tr.links) != 1 || tr.flags[0] {
		t.Fatalf("links = %v flags = %v, want no download", tr.links, tr.flags)
	}
	if len(store.saved) != 1 || store.saved[0].CreatedAt == nil || !store.saved[0].CreatedAt.Equal(old) {
		t.Fatalf("saved = %+v, want one item dated %v", store.saved, old)
	}
}

type adoptTracker struct {
	fakeTracker
	url   string
	err   error
	calls []string
}

func (a *adoptTracker) Adopt(_ context.Context, name, hash string) (string, error) {
	a.calls = append(a.calls, name+"|"+hash)
	return a.url, a.err
}

func submitPageBytes(t *testing.T, comment, entryURL string) (sent, orig []byte) {
	t.Helper()
	orig = buildTestTorrentBytes(t, comment)
	mgr := &magnetTrackingManager{}
	h := &handler{
		manager:        mgr,
		trackerService: &pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: bytes.NewBuffer(append([]byte(nil), orig...))},
	}
	e := &model.Entry{Name: "test", URL: entryURL, TopicID: "123"}
	if err := h.submit(context.Background(), 1, mockPageClient{}, e, nil); err != nil {
		t.Fatalf("submit err = %v", err)
	}
	if !mgr.submitCalled {
		t.Fatal("expected Submit to be called")
	}
	return mgr.lastSubmitData, orig
}

func TestSubmitDownloadAddsMissingComment(t *testing.T) {
	sent, orig := submitPageBytes(t, "", "https://aniliberty.top/anime/releases/release/x")
	mi, err := metainfo.Load(bytes.NewReader(sent))
	if err != nil {
		t.Fatalf("submitted data is not a valid torrent: %v", err)
	}
	if mi.Comment != "https://mock/anime/releases/release/x" {
		t.Errorf("comment = %q, want the release URL", mi.Comment)
	}
	want, _ := metainfo.Load(bytes.NewReader(orig))
	if mi.HashInfoBytes() != want.HashInfoBytes() {
		t.Errorf("infohash changed: %s, want %s", mi.HashInfoBytes(), want.HashInfoBytes())
	}
}

func TestSubmitDownloadKeepsTrackerComment(t *testing.T) {
	sent, orig := submitPageBytes(t, "https://x/t1", "https://other.example/t/9")
	if !bytes.Equal(sent, orig) {
		t.Errorf("a tracker comment must pass byte for byte, got a rewritten file")
	}
}

func TestCollectEntriesAdoptsEmptyComment(t *testing.T) {
	const url = "https://aniliberty.top/anime/releases/release/x"
	tr := &adoptTracker{fakeTracker: fakeTracker{id: 1}, url: url}
	e := &model.Entry{Name: "X - AniLiberty [WEB-DL]", Hash: "h1"}
	h := &handler{manager: &fakeManager{all: []*model.Entry{e}}, storage: &fakeStorage{}, trackerService: tr}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case tk := <-h.idx[1]:
		if tk.entry.URL != url {
			t.Fatalf("queued URL %q, want %q", tk.entry.URL, url)
		}
	default:
		t.Fatal("adopted entry was not queued")
	}
	if len(tr.calls) != 1 || tr.calls[0] != "X - AniLiberty [WEB-DL]|h1" {
		t.Fatalf("Adopt calls = %v", tr.calls)
	}
}

func TestAdoptErrorSkipsPurge(t *testing.T) {
	const gone = "https://rutracker.org/forum/viewtopic.php?t=1"
	goneHash := storagePkg.URLHash(gone)
	store := &fakeStorage{items: map[string]*runnerModel.HistoryItem{goneHash: {URLHash: goneHash, Name: "gone"}}}
	tr := &adoptTracker{fakeTracker: fakeTracker{id: 1}, err: errors.New("api down")}
	h := &handler{
		manager: &fakeManager{all: []*model.Entry{
			{URL: "https://rutracker.org/forum/viewtopic.php?t=2", Name: "alive"},
			{Name: "X - AniLiberty [WEB-DL]", Hash: "h1"},
		}},
		storage:        store,
		trackerService: tr,
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	if len(tr.calls) != 1 {
		t.Fatalf("Adopt calls = %v, want 1", tr.calls)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("history purged after a failed adoption: %v", store.deleted)
	}
}

func TestCommentedEntryNeverAdopted(t *testing.T) {
	tr := &adoptTracker{fakeTracker: fakeTracker{id: 1}, url: "https://x/other"}
	h := &handler{
		manager:        &fakeManager{all: []*model.Entry{{URL: "https://x/t1", Name: "X - AniLiberty", Hash: "h1"}}},
		storage:        &fakeStorage{},
		trackerService: tr,
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	if len(tr.calls) != 0 {
		t.Fatalf("Adopt called for a commented entry: %v", tr.calls)
	}
}

func TestAdoptDefinitiveMissKeepsPurge(t *testing.T) {
	const gone = "https://rutracker.org/forum/viewtopic.php?t=1"
	goneHash := storagePkg.URLHash(gone)
	store := &fakeStorage{items: map[string]*runnerModel.HistoryItem{goneHash: {URLHash: goneHash, Name: "gone"}}}
	tr := &adoptTracker{fakeTracker: fakeTracker{id: 1}}
	h := &handler{
		manager: &fakeManager{all: []*model.Entry{
			{URL: "https://rutracker.org/forum/viewtopic.php?t=2", Name: "alive"},
			{Name: "X - AniLiberty [WEB-DL]", Hash: "h1"},
		}},
		storage:        store,
		trackerService: tr,
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != goneHash {
		t.Fatalf("deleted = %v, want the stale item purged after a definitive miss", store.deleted)
	}
}

func TestPurgeDuplicateNamesFoldsLiveSet(t *testing.T) {
	const u1, u2, u3, u4 = "https://x/t/1", "https://x/t/2", "https://x/t/3", "https://x/t/4"
	store := &fakeStorage{}
	h := &handler{storage: store}
	h.purgeDuplicateNames(context.Background(), []*model.Entry{
		{Name: "Foo", URL: u1},
		{Name: " foo", URL: u2},
		{Name: "Сериал", URL: u3},
		{Name: "сериал", URL: u4},
		{Name: "FOO", URL: u1},
		{Name: "no url"},
	})
	if len(store.purges) != 1 {
		t.Fatalf("PurgeDuplicateNames calls = %d, want 1", len(store.purges))
	}
	want := map[string][]string{
		"foo":    {storagePkg.URLHash(u1), storagePkg.URLHash(u2)},
		"сериал": {storagePkg.URLHash(u3), storagePkg.URLHash(u4)},
	}
	got := store.purges[0]
	if len(got) != len(want) {
		t.Fatalf("live = %v, want keys %v", got, want)
	}
	for k, w := range want {
		g := append([]string(nil), got[k]...)
		slices.Sort(g)
		slices.Sort(w)
		if !slices.Equal(g, w) {
			t.Errorf("live[%q] = %v, want %v", k, g, w)
		}
	}
}

func warnCount(msg string) int {
	n := 0
	for _, e := range logger.Snapshot(1000) {
		if e.Level == "WARN" && e.Message == msg {
			n++
		}
	}
	return n
}

func TestPurgeDuplicateNamesSkippedOnPartialList(t *testing.T) {
	const msg = "duplicate names purge skipped: partial torrent list"
	alive := &model.Entry{URL: "https://rutracker.org/forum/viewtopic.php?t=2", Name: "alive"}
	cases := []struct {
		name string
		mgr  *fakeManager
		tr   *adoptTracker
	}{
		{"list", &fakeManager{all: []*model.Entry{alive}, partialList: true}, &adoptTracker{fakeTracker: fakeTracker{id: 1}}},
		{"adopt", &fakeManager{all: []*model.Entry{alive, {Name: "X - AniLiberty [WEB-DL]", Hash: "h1"}}}, &adoptTracker{fakeTracker: fakeTracker{id: 1}, err: errors.New("api down")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStorage{}
			h := &handler{manager: tc.mgr, storage: store, trackerService: tc.tr}
			h.initTrackerChannels()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			before := warnCount(msg)
			if err := h.push(ctx, false, nil); err != nil {
				t.Fatal(err)
			}
			if len(store.purges) != 0 {
				t.Fatalf("PurgeDuplicateNames called %d times on a partial list", len(store.purges))
			}
			if got := warnCount(msg) - before; got != 1 {
				t.Fatalf("Warn %q count = %d, want 1", msg, got)
			}
		})
	}
}

func TestPurgeDuplicateNamesRunsOnFullList(t *testing.T) {
	store := &fakeStorage{}
	h := &handler{
		manager:        &fakeManager{all: []*model.Entry{{URL: "https://rutracker.org/forum/viewtopic.php?t=2", Name: "alive"}}},
		storage:        store,
		trackerService: &fakeTracker{id: 1},
	}
	h.initTrackerChannels()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.push(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.purges) != 1 {
		t.Fatalf("PurgeDuplicateNames calls = %d, want 1", len(store.purges))
	}
}

func TestWorkerPickDateWinsOverStoredID(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	same := old
	h, store, tr := newPickHandler(pickPageClient{pick: tracker.Pick{DownloadID: "own", Date: &same}})
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "a", URLHash: "abc", TopicID: "sibling", Timestamp: &old, Update: true}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "unchanged" {
		t.Fatalf("logs = %+v, want one unchanged", store.logs)
	}
	if len(tr.links) != 1 || tr.flags[0] {
		t.Fatalf("links = %v flags = %v, want no download", tr.links, tr.flags)
	}
	if len(store.saved) != 1 || store.saved[0].TopicID != "own" {
		t.Fatalf("saved = %+v, want TopicID own", store.saved)
	}
}

func TestWorkerPickGetsSiblings(t *testing.T) {
	const url = "https://pick.example/t/1"
	var got tracker.Held
	h, _, _ := newPickHandler(pickPageClient{pick: tracker.Pick{Miss: "m"}, held: &got})
	h.manager = &fakeManager{all: []*model.Entry{
		{URL: url, Name: "a", Hash: "ha"},
		{URL: url, Name: "b", Hash: "hb"},
		{URL: "https://pick.example/t/2", Name: "c", Hash: "hc"},
	}}
	h.state = NewState()
	h.initTrackerChannels()
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"a": "hb", "b": "ha", "c": ""}
	for len(h.idx[1]) > 0 {
		tk := <-h.idx[1]
		if err := h.worker(context.Background(), 1, tk.entry, nil); err != nil {
			t.Fatal(err)
		}
		if strings.Join(got.Siblings, ",") != want[tk.entry.Name] {
			t.Errorf("%s: Siblings = %v, want %q", tk.entry.Name, got.Siblings, want[tk.entry.Name])
		}
		delete(want, tk.entry.Name)
	}
	if len(want) != 0 {
		t.Fatalf("not queued: %v", want)
	}
}

type failSubmitManager struct {
	fakeManager
	fail string
}

func (m *failSubmitManager) Submit(_ context.Context, _ *bytes.Buffer, opts model.ManagerAddFileOptions) error {
	if opts.Hash == m.fail {
		return errors.New("client down")
	}
	return nil
}

type forceStorage struct {
	loggingStorage
	events []string
}

func (s *forceStorage) SetForceCheck(ctx context.Context, urlHash string, force bool) (bool, error) {
	s.events = append(s.events, fmt.Sprintf("force %s %v", urlHash, force))
	_, _ = s.fakeStorage.SetForceCheck(ctx, urlHash, force)
	return true, nil
}

func (s *forceStorage) SaveItem(ctx context.Context, item *runnerModel.HistoryItem) error {
	s.events = append(s.events, "save "+item.URLHash)
	return s.loggingStorage.SaveItem(ctx, item)
}

func TestSiblingSubmitFailureKeepsForceCheck(t *testing.T) {
	const url = "https://pick.example/t/1"
	urlHash := storagePkg.URLHash(url)
	run := func(order ...string) (*handler, *forceStorage, *failSubmitManager, map[string]*model.Entry) {
		old := time.Now().Add(-48 * time.Hour)
		now := time.Now()
		h, _, _ := newPickHandler(pickPageClient{pick: tracker.Pick{DownloadID: "1", Date: &now}})
		store := &forceStorage{}
		mgr := &failSubmitManager{fail: "ha"}
		h.storage, h.manager = store, mgr
		entries := map[string]*model.Entry{}
		for _, hash := range []string{"ha", "hb"} {
			entries[hash] = &model.Entry{URL: url, Name: hash, Hash: hash, URLHash: urlHash, TopicID: "1", Timestamp: &old, Update: true}
		}
		h.storeSiblings([]*model.Entry{entries["ha"], entries["hb"]}, false)
		for _, hash := range order {
			_ = h.worker(context.Background(), 1, entries[hash], nil)
		}
		return h, store, mgr, entries
	}
	want := "force " + urlHash + " true"
	for _, order := range [][]string{{"ha", "hb"}, {"hb", "ha"}} {
		_, store, _, _ := run(order...)
		if len(store.events) == 0 || store.events[len(store.events)-1] != want {
			t.Errorf("order %v: events = %v, want the row left with force_check", order, store.events)
		}
	}

	h, store, mgr, entries := run("ha")
	mgr.fail = ""
	old := time.Now().Add(-48 * time.Hour)
	entries["ha"].Timestamp = &old
	_ = h.worker(context.Background(), 1, entries["ha"], nil)
	_ = h.worker(context.Background(), 1, entries["hb"], nil)
	if last := store.events[len(store.events)-1]; last != "save "+urlHash {
		t.Errorf("events = %v, want no force_check after the retry succeeded", store.events)
	}
}

func TestSiblingMissKeepsRetryForceCheck(t *testing.T) {
	const url = "https://pick.example/t/1"
	urlHash := storagePkg.URLHash(url)
	added := time.Now().Add(-48 * time.Hour)
	saved := time.Now().Add(time.Hour)
	h, _, tr := newPickHandler(pickPageClient{})
	store := &forceStorage{}
	store.items = map[string]*runnerModel.HistoryItem{urlHash: {
		URLHash: urlHash, TrackerTypeID: 1, TopicID: "1", Exists: true, CreatedAt: &saved, RecheckAt: &saved,
	}}
	mgr := &failSubmitManager{fail: "ha"}
	h.storage, h.manager, h.state = store, mgr, NewState()
	h.initTrackerChannels()
	newEntries := func() []*model.Entry {
		return []*model.Entry{
			{URL: url, Name: "a", Hash: "ha", Timestamp: timePtr(added)},
			{URL: url, Name: "b", Hash: "hb", Timestamp: timePtr(added)},
		}
	}
	first := newEntries()
	for _, e := range first {
		e.URLHash, e.TopicID, e.Update = urlHash, "1", true
	}
	h.storeSiblings(first, false)

	now := time.Now()
	tr.client.pick = tracker.Pick{DownloadID: "1", Date: &now}
	_ = h.worker(context.Background(), 1, first[0], nil)
	tr.client.pick = tracker.Pick{Miss: "unknown"}
	_ = h.worker(context.Background(), 1, first[1], nil)
	if last := store.events[len(store.events)-1]; last != "force "+urlHash+" true" {
		t.Fatalf("events = %v, want force_check kept after the sibling's miss", store.events)
	}

	mgr.fail = ""
	mgr.all = newEntries()
	tr.client.pick = tracker.Pick{DownloadID: "1", Date: &now}
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	store.logs = nil
	for len(h.idx[1]) > 0 {
		if tk := <-h.idx[1]; tk.entry.Hash == "ha" {
			_ = h.worker(context.Background(), 1, tk.entry, nil)
		}
	}
	if len(store.logs) != 1 || store.logs[0].Status != "updated" {
		t.Fatalf("logs = %+v, want A retried and updated on the next run", store.logs)
	}
}

func TestSiblingRetryStopsAtErrorThreshold(t *testing.T) {
	const url = "https://pick.example/t/1"
	urlHash := storagePkg.URLHash(url)
	for _, tc := range []struct {
		threshold, runs int
		order           []string
	}{
		{0, 3, []string{"hb", "ha"}},
		{0, 3, []string{"ha", "hb"}},
		{2, 2, []string{"hb", "ha"}},
	} {
		now := time.Now()
		h, _, _ := newPickHandler(pickPageClient{pick: tracker.Pick{DownloadID: "1", Date: &now}})
		h.config.ErrorStreakThreshold = tc.threshold
		store := &forceStorage{}
		h.storage, h.manager = store, &failSubmitManager{fail: "ha"}
		entries := map[string]*model.Entry{}
		var raw []*model.Entry
		for _, hash := range []string{"ha", "hb"} {
			entries[hash] = &model.Entry{URL: url, Name: hash, Hash: hash, URLHash: urlHash, TopicID: "1", Update: true}
			raw = append(raw, entries[hash])
		}
		h.storeSiblings(raw, false)
		runs := 0
		for forced := true; forced && runs < 10; runs++ {
			for _, hash := range tc.order {
				old := time.Now().Add(-48 * time.Hour)
				entries[hash].Timestamp = &old
				_ = h.worker(context.Background(), 1, entries[hash], nil)
			}
			forced = false
			for _, ev := range store.events {
				forced = ev == "force "+urlHash+" true"
			}
		}
		if runs != tc.runs || h.retryPending(urlHash) {
			t.Errorf("threshold %d order %v: runs = %d pending = %v, want %d runs and no pending retry",
				tc.threshold, tc.order, runs, h.retryPending(urlHash), tc.runs)
		}
	}
}

func TestSiblingRetryKeptOnPartialList(t *testing.T) {
	const url = "https://pick.example/t/1"
	urlHash := storagePkg.URLHash(url)
	now := time.Now()
	h, _, _ := newPickHandler(pickPageClient{pick: tracker.Pick{DownloadID: "1", Date: &now}})
	store := &forceStorage{}
	mgr := &failSubmitManager{fail: "ha"}
	h.storage, h.manager, h.state = store, mgr, NewState()
	h.initTrackerChannels()
	old := time.Now().Add(-48 * time.Hour)
	a := &model.Entry{URL: url, Name: "a", Hash: "ha", URLHash: urlHash, TopicID: "1", Timestamp: &old, Update: true}
	b := &model.Entry{URL: url, Name: "b", Hash: "hb", URLHash: urlHash, TopicID: "1", Timestamp: &old, Update: true}
	h.storeSiblings([]*model.Entry{a, b}, false)
	_ = h.worker(context.Background(), 1, a, nil)

	mgr.all = []*model.Entry{{URL: url, Name: "b", Hash: "hb"}}
	mgr.partialList = true
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if !h.retryPending(urlHash) {
		t.Fatal("retry dropped after a partial list without the torrent")
	}
	mgr.partialList = false
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if h.retryPending(urlHash) {
		t.Fatal("retry kept after a complete list without the torrent")
	}
}

func (f *fakeStorage) ErrorMarks(context.Context) (map[string]storageModel.ErrorMark, error) {
	return f.marks, f.marksErr
}

func TestLogCheckErrorSendsCheckFailed(t *testing.T) {
	var kinds []string
	var mu sync.Mutex
	got := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Event string `json:"event"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		kinds = append(kinds, body.Event)
		mu.Unlock()
		got <- struct{}{}
	}))
	defer srv.Close()

	newHandler := func(events []string) *handler {
		return &handler{
			config:   model.HandlerConfig{ErrorStreakThreshold: 100},
			storage:  &loggingStorage{},
			notifier: notify.New(notify.Config{WebhookURL: srv.URL, Events: events, Enabled: true}, ""),
		}
	}
	e := &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc"}
	ctx := context.Background()
	seen := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), kinds...)
	}

	// без подписки (nil-список) check_failed не уходит
	newHandler(nil).logCheckStreak(ctx, e, 1, "error", "boom", false, nil)
	select {
	case <-got:
		t.Fatal("check_failed delivered without subscription")
	case <-time.After(150 * time.Millisecond):
	}

	h := newHandler([]string{notify.KindCheckFailed})
	h.logCheckStreak(ctx, e, 1, "error", "boom", true, nil)
	h.logCheckStreak(ctx, e, 1, "error", "boom", false, &dryReport{})
	h.logCheckStreak(ctx, e, 1, "unchanged", "", false, nil)
	select {
	case <-got:
		t.Fatalf("unexpected event for tracker-wide, dry-run or non-error: %v", seen())
	case <-time.After(150 * time.Millisecond):
	}

	h.logCheckStreak(ctx, e, 1, "error", "boom", false, nil)
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("check_failed not delivered to subscribed webhook")
	}
	if k := seen(); len(k) != 1 || k[0] != notify.KindCheckFailed {
		t.Fatalf("events = %v, want [check_failed]", k)
	}
}

func TestLogCheckCountsMetric(t *testing.T) {
	const ttid = 99001
	if _, ok := registry.ByID(ttid); !ok {
		registry.Register(registry.Def{ID: ttid, Key: "metrics_probe", New: func(tracker.Tracker) tracker.ITracker { return nil }})
	}
	e := &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc"}
	h := &handler{config: model.HandlerConfig{ErrorStreakThreshold: 100}, storage: &loggingStorage{}}

	known := metrics.Checks.Value("metrics_probe", "unchanged")
	unknown := metrics.Checks.Value("unknown", "error")

	h.logCheck(context.Background(), e, ttid, "unchanged", "", nil)
	if got := metrics.Checks.Value("metrics_probe", "unchanged"); got != known+1 {
		t.Fatalf("known tracker counter = %d, want %d", got, known+1)
	}
	h.logCheckStreak(context.Background(), e, 424242, "error", "boom", true, nil)
	if got := metrics.Checks.Value("unknown", "error"); got != unknown+1 {
		t.Fatalf("unregistered tracker counter = %d, want %d", got, unknown+1)
	}

	h.logCheck(context.Background(), e, ttid, "unchanged", "", &dryReport{})
	if got := metrics.Checks.Value("metrics_probe", "unchanged"); got != known+1 {
		t.Fatalf("dry-run must not count, got %d want %d", got, known+1)
	}
}

func newReleaseHandler(date time.Time) (*handler, *loggingStorage) {
	h, store, _ := newPickHandler(pickPageClient{pick: tracker.Pick{DownloadID: "1", Date: &date}})
	return h, store
}

func TestWorkerRecordsReleaseTime(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	d := time.Now().Add(-2 * time.Hour)
	h, store := newReleaseHandler(d)
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", TopicID: "1", Timestamp: &old, Update: true}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "updated" {
		t.Fatalf("logs = %+v, want one updated", store.logs)
	}
	if len(store.releases) != 1 {
		t.Fatalf("release calls = %+v, want one", store.releases)
	}
	if c := store.releases[0]; c.hash != "abc" || !c.at.Equal(d) || c.keep != 8 {
		t.Fatalf("release call = %+v, want abc %v keep 8", c, d)
	}
}

func TestWorkerNoReleaseTimeWithoutPageDate(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	h, store, _ := newDownloadIDHandler(time.Now().Add(-2 * time.Hour))
	// смена id темы: дата берется из time.Now(), а не со страницы
	e := &model.Entry{URL: "https://nnm.example/t/830137", Name: "test", URLHash: "abc", TopicID: "830137", Timestamp: &old, Update: true}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "updated" {
		t.Fatalf("logs = %+v, want one updated", store.logs)
	}
	if len(store.releases) != 0 {
		t.Fatalf("release calls = %+v, want none", store.releases)
	}
}

func TestWorkerNoReleaseTimeWhenUnchanged(t *testing.T) {
	d := time.Now().Add(-2 * time.Hour)
	h, store := newReleaseHandler(d)
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", TopicID: "1", Timestamp: &d, Update: true}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "unchanged" {
		t.Fatalf("logs = %+v, want one unchanged", store.logs)
	}
	if len(store.releases) != 0 {
		t.Fatalf("release calls = %+v, want none", store.releases)
	}
}

func TestWorkerNoReleaseTimeOnDryRun(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	h, store := newReleaseHandler(time.Now().Add(-2 * time.Hour))
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", TopicID: "1", Timestamp: &old, Update: true}
	rep := &dryReport{}
	if err := h.worker(context.Background(), 1, e, rep); err != nil {
		t.Fatal(err)
	}
	if got := rep.updated.Load(); got != 1 {
		t.Fatalf("dry updated = %d, want 1", got)
	}
	if len(store.releases) != 0 || len(store.saved) != 0 {
		t.Fatalf("release calls = %+v, saved = %d, want none", store.releases, len(store.saved))
	}
}

func TestWorkerReleaseTimeErrorWarns(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	h, store := newReleaseHandler(time.Now().Add(-2 * time.Hour))
	store.releaseErr = errors.New("boom")
	const msg = "release time of test not saved: boom"
	before := countLogged(msg)
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", TopicID: "1", Timestamp: &old, Update: true}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if got := countLogged(msg) - before; got != 1 {
		t.Fatalf("WARN %q logged %d times, want 1", msg, got)
	}
	if len(store.logs) != 1 || store.logs[0].Status != "updated" {
		t.Fatalf("logs = %+v, want one updated", store.logs)
	}
	if len(store.saved) != 1 {
		t.Fatalf("saved = %d, want 1", len(store.saved))
	}
}

type fastTracker struct {
	safeFakeTracker
	calls atomic.Int32
}

func (f *fastTracker) GetHourLimit(int) int { return 3600000 }

func (f *fastTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	f.calls.Add(1)
	return nil, fmt.Errorf("test skip")
}

func newQueueHandler(url string) (*handler, *fastTracker, *State) {
	state := NewState()
	tr := &fastTracker{safeFakeTracker: safeFakeTracker{id: 1}}
	h := &handler{
		manager:        &fakeManager{all: []*model.Entry{{URL: url, Name: "x"}}},
		storage:        &fakeStorage{},
		trackerService: tr,
		state:          state,
		gen:            state.Reset(),
	}
	h.initTrackerChannels()
	return h, tr, state
}

func runWorkerUntilIdle(t *testing.T, h *handler, state *State) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cherr := make(chan error, 8)
	go h.trackerWorker(ctx, 1, cherr)
	deadline := time.Now().Add(2 * time.Second)
	for state.Running() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if state.Running() {
		t.Fatal("queue did not drain")
	}
}

func TestStaleTaskSkippedAfterForcedCheck(t *testing.T) {
	const url = "https://example.com/t/1"
	h, tr, state := newQueueHandler(url)
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	forced := map[string]struct{}{storagePkg.URLHash(url): {}}
	if err := h.push(context.Background(), false, forced); err != nil {
		t.Fatal(err)
	}
	runWorkerUntilIdle(t, h, state)
	if got := tr.calls.Load(); got != 1 {
		t.Fatalf("GetCachedClient calls = %d, want 1: the stale queued task must be skipped", got)
	}
}

func TestForcedDoubleClickRunsOnce(t *testing.T) {
	const url = "https://example.com/t/1"
	h, tr, state := newQueueHandler(url)
	forced := map[string]struct{}{storagePkg.URLHash(url): {}}
	for range 2 {
		if err := h.push(context.Background(), false, forced); err != nil {
			t.Fatal(err)
		}
	}
	runWorkerUntilIdle(t, h, state)
	if got := tr.calls.Load(); got != 1 {
		t.Fatalf("GetCachedClient calls = %d, want 1", got)
	}
}

type submitCountManager struct {
	fakeManager
	submits int
}

func (m *submitCountManager) Submit(context.Context, *bytes.Buffer, model.ManagerAddFileOptions) error {
	m.submits++
	return nil
}

func TestWorkerDefersUnhealthyTorrent(t *testing.T) {
	page := bytes.NewBufferString(strings.Repeat("x", 60))
	store := &loggingStorage{}
	mgr := &submitCountManager{}
	old := time.Now().Add(-48 * time.Hour)
	h := &handler{
		config:  model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage: store,
		manager: mgr,
		trackerService: &updatingPageTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: page},
		},
		mx: helper.KeyedMutex{},
	}
	e := &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc", Timestamp: &old, Unhealthy: true}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if mgr.submits != 0 {
		t.Fatalf("Submit calls = %d, want 0 for a busy torrent", mgr.submits)
	}
	if len(store.saved) != 0 {
		t.Fatalf("history saved %d times, want 0 so the next pass retries", len(store.saved))
	}
	if len(store.logs) != 1 {
		t.Fatalf("logs = %d, want 1", len(store.logs))
	}
	l := store.logs[0]
	if l.Status != "error" || !l.KeepStreak || !strings.Contains(l.Error, "update postponed") {
		t.Fatalf("log = %+v, want error with KeepStreak and the busy text", l)
	}
}

func TestSiblingTorrentsOfOneTopicBothRun(t *testing.T) {
	const url = "https://example.com/t/1"
	h, tr, state := newQueueHandler(url)
	h.manager = &fakeManager{all: []*model.Entry{{URL: url, Name: "a", Hash: "ha"}, {URL: url, Name: "b", Hash: "hb"}}}
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	runWorkerUntilIdle(t, h, state)
	if got := tr.calls.Load(); got != 2 {
		t.Fatalf("GetCachedClient calls = %d, want 2: both sibling torrents of one topic must be checked", got)
	}
}

func TestBusySiblingKeepsRetryWithoutFailCount(t *testing.T) {
	const url = "https://example.com/t/1"
	page := bytes.NewBufferString(strings.Repeat("x", 60))
	urlHash := storagePkg.URLHash(url)
	store := &loggingStorage{fakeStorage: fakeStorage{items: map[string]*runnerModel.HistoryItem{
		url: {URLHash: urlHash, Exists: true},
	}}}
	h := &handler{
		config:  model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1, ErrorStreakThreshold: 3},
		storage: store,
		manager: &submitCountManager{},
		trackerService: &updatingPageTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: page},
		},
		mx: helper.KeyedMutex{},
	}
	h.storeSiblings([]*model.Entry{{URL: url, Hash: "ha"}, {URL: url, Hash: "hb"}}, false)
	old := time.Now().Add(-48 * time.Hour)
	for pass := range 5 {
		store.items[url].ForceCheck = false
		e := &model.Entry{URL: url, Name: "a", Hash: "ha", URLHash: urlHash, Timestamp: &old, Unhealthy: true}
		if err := h.worker(context.Background(), 1, e, nil); err != nil {
			t.Fatal(err)
		}
		if !store.items[url].ForceCheck || !h.retryPending(urlHash) {
			t.Fatalf("pass %d: force_check = %v, retryPending = %v, want both true so the busy torrent is retried", pass, store.items[url].ForceCheck, h.retryPending(urlHash))
		}
	}
}

type serverErrTracker struct {
	pageTracker
	code int
}

func (s *serverErrTracker) Response(context.Context, int, string, bool) (*bytes.Buffer, error) {
	return nil, fmt.Errorf("get page error: %w", &client.StatusError{Code: s.code, Msg: "status"})
}

func TestTrackerServerErrorKeepsStreak(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	for _, tc := range []struct {
		code       int
		keepStreak bool
	}{{503, true}, {500, true}, {403, false}, {404, false}} {
		store := &loggingStorage{fakeStorage: fakeStorage{checkStreak: 5}}
		h := &handler{
			config:  model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1, ErrorStreakThreshold: 3},
			storage: store,
			manager: &fakeManager{},
			trackerService: &serverErrTracker{
				pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}},
				code:        tc.code,
			},
		}
		e := &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc", Timestamp: &old}
		_ = h.worker(context.Background(), 1, e, nil)
		if len(store.logs) != 1 || store.logs[0].Status != "error" || store.logs[0].KeepStreak != tc.keepStreak {
			t.Fatalf("code %d: logs = %+v, want one error with KeepStreak=%v", tc.code, store.logs, tc.keepStreak)
		}
		if got := len(store.ignoreCalls) == 1; got == tc.keepStreak {
			t.Fatalf("code %d: ignoreCalls = %v, auto-ignore must happen exactly when KeepStreak is false", tc.code, store.ignoreCalls)
		}
	}
}

type byURLTracker struct {
	pageTracker
	codes map[string]int
}

func (b *byURLTracker) Response(_ context.Context, _ int, link string, _ bool) (*bytes.Buffer, error) {
	if code := b.codes[link]; code != 0 {
		return nil, fmt.Errorf("get page error: %w", &client.StatusError{Code: code, Msg: "status"})
	}
	return b.page, nil
}

type streakStorage struct {
	loggingStorage
	streaks map[string]int
}

func (s *streakStorage) AddCheckLog(_ context.Context, e *storageModel.CheckLogEntry) (int, error) {
	s.logs = append(s.logs, *e)
	switch {
	case e.Status != "error":
		s.streaks[e.URLHash] = 0
	case !e.KeepStreak:
		s.streaks[e.URLHash]++
	}
	return s.streaks[e.URLHash], nil
}

func serverErrHandler(threshold int) (*handler, *byURLTracker, *streakStorage) {
	tr := &byURLTracker{
		pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: bytes.NewBufferString(strings.Repeat("x", 60))},
		codes:       map[string]int{},
	}
	store := &streakStorage{streaks: map[string]int{}}
	h := &handler{
		config:         model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1, ErrorStreakThreshold: threshold, ListRefreshPerDay: 2},
		storage:        store,
		manager:        &fakeManager{},
		trackerService: tr,
		mx:             helper.KeyedMutex{},
		state:          NewState(),
	}
	return h, tr, store
}

func releaseEntry(name string) *model.Entry {
	return &model.Entry{URL: "https://example.com/" + name, Name: name, URLHash: name}
}

func checkRelease(t *testing.T, h *handler, tr *byURLTracker, name string, code int, rep *dryReport) {
	t.Helper()
	e := releaseEntry(name)
	tr.codes[e.URL] = code
	if err := h.worker(context.Background(), 1, e, rep); err != nil && code == 0 {
		t.Fatal(err)
	}
}

func TestIsolatedServerErrorPausesRelease(t *testing.T) {
	h, tr, _ := serverErrHandler(3)
	tr.codes[releaseEntry("B").URL] = 503
	for range 2 {
		checkRelease(t, h, tr, "A", 0, nil)
		checkRelease(t, h, tr, "B", 503, nil)
	}
	p := &pushPass{}
	if !h.backedOff(p, releaseEntry("B")) {
		t.Fatal("B answered 503 twice while the tracker served A, want a one slot pause")
	}
	if h.backedOff(p, releaseEntry("A")) {
		t.Fatal("A answers fine, must not be backed off")
	}
}

func TestServerErrorRunThenOtherErrorNoIgnore(t *testing.T) {
	h, tr, store := serverErrHandler(2)
	n, hits := notifyProbe(t)
	h.notifier = n
	for range 3 {
		checkRelease(t, h, tr, "A", 0, nil)
		checkRelease(t, h, tr, "B", 503, nil)
	}
	for _, l := range store.logs {
		if l.URLHash == "B" && (l.Status != "error" || !l.KeepStreak) {
			t.Fatalf("503 log = %+v, want an error with KeepStreak", l)
		}
	}
	select {
	case <-hits:
		t.Fatal("CheckFailed sent for a 503")
	case <-time.After(150 * time.Millisecond):
	}
	checkRelease(t, h, tr, "B", 403, nil)
	if store.streaks["B"] != 1 || len(store.ignoreCalls) != 0 {
		t.Fatalf("streak = %d, ignoreCalls = %v, want 1 and none: 503s must not count", store.streaks["B"], store.ignoreCalls)
	}
	checkRelease(t, h, tr, "B", 403, nil)
	if len(store.ignoreCalls) != 1 || store.ignoreCalls[0] != "B" {
		t.Fatalf("ignoreCalls = %v, want [B] after the second 403", store.ignoreCalls)
	}
}

func TestServerErrorOutagePausesNothing(t *testing.T) {
	h, tr, _ := serverErrHandler(3)
	checkRelease(t, h, tr, "A", 0, nil)
	for _, name := range []string{"B", "C", "D"} {
		checkRelease(t, h, tr, name, 503, nil)
	}
	for name, want := range map[string]bool{"B": true, "C": false, "D": false} {
		m, ok := h.state.ServerErrorMark(name)
		if ok != want || (ok && m.Streak != 1) {
			t.Fatalf("%s: mark = %+v ok = %v, want counted = %v with count 1", name, m, ok, want)
		}
	}
	for _, name := range []string{"B", "C", "D"} {
		if h.backedOff(&pushPass{}, releaseEntry(name)) {
			t.Fatalf("%s paused by an outage", name)
		}
	}
}

func TestServerErrorPauseClearedByPageSuccess(t *testing.T) {
	h, tr, _ := serverErrHandler(3)
	for range 2 {
		checkRelease(t, h, tr, "A", 0, nil)
		checkRelease(t, h, tr, "B", 503, nil)
	}
	checkRelease(t, h, tr, "B", 0, nil)
	if _, ok := h.state.ServerErrorMark("B"); ok || h.backedOff(&pushPass{}, releaseEntry("B")) {
		t.Fatal("B page success must clear its pause")
	}
}

func TestServerErrorForcedItemNotBackedOff(t *testing.T) {
	h, tr, _ := serverErrHandler(3)
	for range 2 {
		checkRelease(t, h, tr, "A", 0, nil)
		checkRelease(t, h, tr, "B", 503, nil)
	}
	p := &pushPass{items: map[string]*runnerModel.HistoryItem{"B": {ForceCheck: true}}}
	if h.backedOff(p, releaseEntry("B")) {
		t.Fatal("a forced item must bypass the 5xx pause")
	}
	if !h.backedOff(&pushPass{}, releaseEntry("B")) {
		t.Fatal("without the force flag B stays paused")
	}
}

func TestServerErrorNilStateNoPanic(t *testing.T) {
	h, tr, _ := serverErrHandler(3)
	h.state = nil
	checkRelease(t, h, tr, "A", 0, nil)
	checkRelease(t, h, tr, "B", 503, nil)
	if h.backedOff(&pushPass{}, releaseEntry("B")) {
		t.Fatal("no state, no pause")
	}
}

func TestDryRunServerErrorLeavesStateUntouched(t *testing.T) {
	for _, realFirst := range []bool{false, true} {
		h, tr, store := serverErrHandler(3)
		first := &dryReport{}
		if realFirst {
			first = nil
		}
		checkRelease(t, h, tr, "A", 0, first)
		checkRelease(t, h, tr, "B", 503, &dryReport{})
		if _, ok := h.state.ServerErrorMark("B"); ok {
			t.Fatalf("realFirst=%v: dry-run 503 counted", realFirst)
		}
		if len(store.logs) > 1 {
			t.Fatalf("realFirst=%v: dry-run wrote check logs: %+v", realFirst, store.logs)
		}
		checkRelease(t, h, tr, "A", 0, nil)
		checkRelease(t, h, tr, "B", 503, nil)
		if m, ok := h.state.ServerErrorMark("B"); !ok || m.Streak != 1 {
			t.Fatalf("realFirst=%v: mark = %+v ok = %v, want count 1 from the real worker only", realFirst, m, ok)
		}
		if h.backedOff(&pushPass{}, releaseEntry("B")) {
			t.Fatalf("realFirst=%v: B backed off after a single counted 5xx", realFirst)
		}
	}
}

func TestDryRunPageSuccessDoesNotMarkTrackerAnswered(t *testing.T) {
	h, tr, _ := serverErrHandler(3)
	checkRelease(t, h, tr, "A", 0, &dryReport{})
	checkRelease(t, h, tr, "B", 503, nil)
	if _, ok := h.state.ServerErrorMark("B"); ok {
		t.Fatal("a dry-run page success must not make the next 5xx isolated")
	}
}

func TestServerErrorOnlyReleaseBackedOffWithoutMark(t *testing.T) {
	h, tr, _ := serverErrHandler(3)
	for range 2 {
		checkRelease(t, h, tr, "A", 0, nil)
		checkRelease(t, h, tr, "B", 503, nil)
	}
	p := &pushPass{marks: map[string]storageModel.ErrorMark{}}
	if !h.backedOff(p, releaseEntry("B")) {
		t.Fatal("B has count 2 and no error mark, want backed off")
	}
}

func TestServerError403AfterSuccessStillIgnores(t *testing.T) {
	h, tr, store := serverErrHandler(2)
	for range 2 {
		checkRelease(t, h, tr, "A", 0, nil)
		checkRelease(t, h, tr, "B", 403, nil)
	}
	if len(store.ignoreCalls) != 1 || store.ignoreCalls[0] != "B" {
		t.Fatalf("ignoreCalls = %v, want [B]", store.ignoreCalls)
	}
}

func TestServerErrorPauseIgnoredByDryAndForcedPass(t *testing.T) {
	h, tr, _ := serverErrHandler(3)
	for range 2 {
		checkRelease(t, h, tr, "A", 0, nil)
		checkRelease(t, h, tr, "B", 503, nil)
	}
	if h.backedOff(&pushPass{dry: true}, releaseEntry("B")) {
		t.Fatal("a dry-run pass must not skip a release for the 5xx pause")
	}
	forced := &pushPass{forced: map[string]struct{}{"B": {}}, items: map[string]*runnerModel.HistoryItem{"B": {}}}
	if h.backedOff(forced, releaseEntry("B")) {
		t.Fatal("a forced pass must not skip a release for the 5xx pause")
	}
}

type blockTracker struct {
	fastTracker
	in   chan struct{}
	gate chan struct{}
}

func (b *blockTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	b.in <- struct{}{}
	<-b.gate
	return nil, fmt.Errorf("test skip")
}

func TestNormalRunningOnlyForNormalPass(t *testing.T) {
	const url = "https://example.com/t/1"
	forcedSet := map[string]struct{}{storagePkg.URLHash(url): {}}
	for _, tc := range []struct {
		name       string
		dry        bool
		forced     map[string]struct{}
		wantNormal bool
	}{
		{"normal", false, nil, true},
		{"forced", false, forcedSet, false},
		{"dry", true, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, state := newQueueHandler(url)
			bt := &blockTracker{in: make(chan struct{}, 4), gate: make(chan struct{})}
			bt.id = 1
			h.trackerService = bt
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := h.push(ctx, tc.dry, tc.forced); err != nil {
				t.Fatal(err)
			}
			cherr := make(chan error, 8)
			go h.trackerWorker(ctx, 1, cherr)
			select {
			case <-bt.in:
			case <-time.After(2 * time.Second):
				t.Fatal("worker did not start the task")
			}
			if !state.Running() {
				t.Fatal("pass must be running while its task runs")
			}
			if got := state.NormalRunning(); got != tc.wantNormal {
				t.Fatalf("NormalRunning = %v while the task runs, want %v", got, tc.wantNormal)
			}
			close(bt.gate)
			deadline := time.Now().Add(2 * time.Second)
			for state.Running() && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if state.Running() || state.NormalRunning() {
				t.Fatalf("after the pass: Running = %v, NormalRunning = %v, want both false", state.Running(), state.NormalRunning())
			}
		})
	}
}

type loginForcingTracker struct {
	*pickTracker
	forced []int
}

func (l *loginForcingTracker) ForceLoginNext(ttid int) { l.forced = append(l.forced, ttid) }

func TestWorkerMissForcesLoginNext(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour)
	d := time.Now().Add(-time.Hour)
	cases := []struct {
		name string
		pick tracker.Pick
		rep  *dryReport
		want int
	}{
		{"pick miss", tracker.Pick{Miss: "no link for guests"}, nil, 1},
		{"pick miss dry run", tracker.Pick{Miss: "no link for guests"}, &dryReport{}, 0},
		{"date found", tracker.Pick{Date: &d}, nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, tr := newPickHandler(pickPageClient{pick: tc.pick})
			rec := &loginForcingTracker{pickTracker: tr}
			h.trackerService = rec
			e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc", TopicID: "1", Timestamp: &old, Update: true}
			if err := h.worker(context.Background(), 1, e, tc.rep); err != nil {
				t.Fatal(err)
			}
			if len(rec.forced) != tc.want || (tc.want == 1 && rec.forced[0] != 1) {
				t.Fatalf("forced = %v, want %d call(s) for tracker 1", rec.forced, tc.want)
			}
		})
	}
}

func TestWorkerDateMissingForcesLoginNext(t *testing.T) {
	h, _, tr := newPickHandler(pickPageClient{})
	rec := &loginForcingTracker{pickTracker: tr}
	h.trackerService = rec
	e := &model.Entry{URL: "https://pick.example/t/1", Name: "test", URLHash: "abc"}
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if len(rec.forced) != 1 {
		t.Fatalf("forced = %v, want one call when the page has no date", rec.forced)
	}
}

func drainQueued(ch chan *task) []string {
	var urls []string
	for {
		select {
		case tk := <-ch:
			urls = append(urls, tk.entry.URL)
		default:
			return urls
		}
	}
}

func newHoldHandler(store storage, mgr manager, cfg model.HandlerConfig) *handler {
	h := &handler{config: cfg, manager: mgr, storage: store, trackerService: &fakeTracker{id: 1}}
	h.initTrackerChannels()
	return h
}

func TestPushSkipsHeldNewRelease(t *testing.T) {
	const held = "https://example.com/t/1"
	const free = "https://example.com/t/2"
	store := &fakeStorage{holds: map[string]time.Time{storagePkg.URLHash(held): time.Now().Add(time.Hour)}}
	h := newHoldHandler(store, &fakeManager{all: []*model.Entry{
		{URL: held, Name: "held"},
		{URL: free, Name: "free"},
	}}, model.HandlerConfig{})
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if got := drainQueued(h.idx[1]); !slices.Equal(got, []string{free}) {
		t.Fatalf("queued %v, want [%s]", got, free)
	}
	if store.holdsCalls != 1 {
		t.Fatalf("holds read %d times, want 1", store.holdsCalls)
	}
}

func TestPushQueuesDueHold(t *testing.T) {
	const due = "https://example.com/t/1"
	store := &fakeStorage{holds: map[string]time.Time{storagePkg.URLHash(due): time.Now().Add(-time.Minute)}}
	h := newHoldHandler(store, &fakeManager{all: []*model.Entry{{URL: due, Name: "due"}}}, model.HandlerConfig{})
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if got := drainQueued(h.idx[1]); !slices.Equal(got, []string{due}) {
		t.Fatalf("queued %v, want [%s]", got, due)
	}
}

func TestPushForcedIgnoresHold(t *testing.T) {
	const held = "https://example.com/t/1"
	hash := storagePkg.URLHash(held)
	store := &fakeStorage{holds: map[string]time.Time{hash: time.Now().Add(time.Hour)}}
	h := newHoldHandler(store, &fakeManager{all: []*model.Entry{{URL: held, Name: "held"}}}, model.HandlerConfig{})
	if err := h.push(context.Background(), false, map[string]struct{}{hash: {}}); err != nil {
		t.Fatal(err)
	}
	if got := drainQueued(h.pri[1]); !slices.Equal(got, []string{held}) {
		t.Fatalf("queued %v, want [%s]", got, held)
	}
}

func TestPushDryIgnoresHolds(t *testing.T) {
	const held = "https://example.com/t/1"
	store := &fakeStorage{holds: map[string]time.Time{storagePkg.URLHash(held): time.Now().Add(time.Hour)}}
	h := newHoldHandler(store, &fakeManager{all: []*model.Entry{{URL: held, Name: "held"}}}, model.HandlerConfig{})
	h.state = NewState()
	h.gen = h.state.Reset()
	if err := h.push(context.Background(), true, nil); err != nil {
		t.Fatal(err)
	}
	if got := drainQueued(h.idx[1]); !slices.Equal(got, []string{held}) {
		t.Fatalf("queued %v, want [%s]", got, held)
	}
	if store.holdsCalls != 0 || len(store.droppedNew) != 0 {
		t.Fatalf("dry pass: holds read %d times, dropped %v", store.holdsCalls, store.droppedNew)
	}
}

func TestPushPurgesStaleHolds(t *testing.T) {
	const a = "https://example.com/t/1"
	const b = "https://example.com/t/2"
	const c = "https://example.com/t/3"
	const e = "https://example.com/t/5"
	ha, hb, hc, he := storagePkg.URLHash(a), storagePkg.URLHash(b), storagePkg.URLHash(c), storagePkg.URLHash(e)
	later := time.Now().Add(time.Hour)
	store := &fakeStorage{
		holds: map[string]time.Time{ha: later, hb: later, hc: later, he: later},
		items: map[string]*runnerModel.HistoryItem{he: {URLHash: he, Name: "e", TrackerTypeID: 1, RecheckAt: timePtr(later)}},
	}
	h := newHoldHandler(store, &fakeManager{all: []*model.Entry{
		{URL: a, Name: "a", Category: "tv"},
		{URL: c, Name: "c", Category: "movies"},
		{URL: e, Name: "e", Category: "movies"},
	}}, model.HandlerConfig{Categories: []string{"tv"}})
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	got := slices.Clone(store.droppedNew)
	slices.Sort(got)
	want := []string{hb, hc}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("dropped %v, want %v", got, want)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("history deleted %v, want none", store.deleted)
	}
}

func TestPushKeepsHoldsOnPartialList(t *testing.T) {
	const alive = "https://example.com/t/1"
	store := &fakeStorage{holds: map[string]time.Time{storagePkg.URLHash("https://example.com/t/2"): time.Now().Add(time.Hour)}}
	h := newHoldHandler(store, &fakeManager{all: []*model.Entry{{URL: alive, Name: "alive"}}, partialList: true}, model.HandlerConfig{})
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if len(store.droppedNew) != 0 {
		t.Fatalf("dropped %v on a partial list, want none", store.droppedNew)
	}
}

func TestPushHoldsReadErrorIgnoresHolds(t *testing.T) {
	const held = "https://example.com/t/1"
	store := &fakeStorage{
		holds: map[string]time.Time{
			storagePkg.URLHash(held):                      time.Now().Add(time.Hour),
			storagePkg.URLHash("https://example.com/t/2"): time.Now().Add(time.Hour),
		},
		holdsErr: errors.New("db locked"),
	}
	h := newHoldHandler(store, &fakeManager{all: []*model.Entry{{URL: held, Name: "held"}}}, model.HandlerConfig{})
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if got := drainQueued(h.idx[1]); !slices.Equal(got, []string{held}) {
		t.Fatalf("queued %v, want [%s]", got, held)
	}
	if len(store.droppedNew) != 0 {
		t.Fatalf("dropped %v after a holds read error, want none", store.droppedNew)
	}
}

type orderStorage struct {
	*fakeStorage
	log *[]string
}

func (s orderStorage) NewReleaseHolds(ctx context.Context) (map[string]time.Time, error) {
	*s.log = append(*s.log, "holds")
	return s.fakeStorage.NewReleaseHolds(ctx)
}

type orderManager struct {
	*fakeManager
	log *[]string
}

func (m orderManager) GetEntries(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error) {
	*m.log = append(*m.log, "list")
	return m.fakeManager.GetEntries(ctx, opts)
}

func TestPushReadsHoldsBeforeList(t *testing.T) {
	var log []string
	h := newHoldHandler(orderStorage{fakeStorage: &fakeStorage{}, log: &log},
		orderManager{fakeManager: &fakeManager{all: []*model.Entry{{URL: "https://example.com/t/1", Name: "a"}}}, log: &log},
		model.HandlerConfig{})
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(log, []string{"holds", "list"}) {
		t.Fatalf("calls %v, want [holds list]", log)
	}
}
