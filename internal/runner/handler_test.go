package runner

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"trackerr/internal/helper"
	"trackerr/internal/model"
	proxyModel "trackerr/internal/model/proxy"
	runnerModel "trackerr/internal/model/runner"
	storageModel "trackerr/internal/model/storage"
	"trackerr/internal/model/tracker"
	storagePkg "trackerr/internal/storage"
	"trackerr/internal/tracker/client"
)

func timePtr(t time.Time) *time.Time { return &t }

type fakeStorage struct {
	ignore      map[string]struct{}
	items       map[string]*runnerModel.HistoryItem
	deleted     []string
	checkStreak int
	ignoreCalls []string
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

func (f *fakeStorage) PurgeDuplicateNames(context.Context, map[string]string) error { return nil }

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

func TestGetRecheckAt(t *testing.T) {
	h := &handler{config: model.HandlerConfig{
		ThresholdMonth:    1,
		ThresholdHalfYear: 7,
		ThresholdYear:     30,
		ThresholdOld:      90,
	}}

	cases := []struct {
		name     string
		tm       *time.Time
		wantDays int
	}{
		{"nil timestamp", nil, 1},
		{"zero-value year", timePtr(time.Time{}), 1},
		{"under a month old", timePtr(time.Now().Add(-24 * time.Hour)), 1},
		{"a couple months old", timePtr(time.Now().Add(-60 * 24 * time.Hour)), 7},
		{"under a year old", timePtr(time.Now().Add(-200 * 24 * time.Hour)), 30},
		{"over a year old", timePtr(time.Now().Add(-400 * 24 * time.Hour)), 90},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := h.getRecheckAt(c.tm)
			if got == nil {
				t.Fatal("expected non-nil result")
			}
			want := time.Now().AddDate(0, 0, c.wantDays)
			if diff := got.Sub(want); diff < -time.Minute || diff > time.Minute {
				t.Errorf("getRecheckAt(%v) = %v, want ~%v (+%d days)", c.tm, got, want, c.wantDays)
			}
		})
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

func (mockPageClient) GetName() string                                          { return "mock" }
func (mockPageClient) GetDomain() string                                        { return "mock" }
func (mockPageClient) GetHost() string                                          { return "mock" }
func (mockPageClient) GetProxyType() proxyModel.ProxyType                       { return proxyModel.TypeNone }
func (mockPageClient) GetPublicity() string                                     { return "" }
func (mockPageClient) GetHourLimit() int                                        { return 1 }
func (mockPageClient) GetCookies() []*http.Cookie                               { return nil }
func (mockPageClient) GetCredentials() string                                   { return "" }
func (mockPageClient) GetMirrors() []string                                     { return nil }
func (mockPageClient) LoginLink() string                                        { return "" }
func (mockPageClient) DownloadLink(string) string                               { return "" }
func (mockPageClient) TopicLink(string) string                                  { return "" }
func (mockPageClient) Detect(string) bool                                       { return true }
func (mockPageClient) Headers() *http.Header                                    { return &http.Header{} }
func (mockPageClient) ParseDate(*bytes.Buffer, bool) *time.Time                 { return nil }
func (mockPageClient) ParseDownloadID(*bytes.Buffer, bool) string               { return "" }
func (mockPageClient) ParseMagnet(*bytes.Buffer, bool) string                   { return "" }
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

func (m *errAlreadyAddedManager) SubmitMagnet(context.Context, string, model.ManagerAddFileOptions) error {
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

func (m *submitTrackingManager) SubmitMagnet(context.Context, string, model.ManagerAddFileOptions) error {
	m.submitted = true
	return nil
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
		t.Fatal("expected SubmitMagnet to be called")
	}
}

func TestWorkerUnchangedWithoutForceCheckSameDates(t *testing.T) {
	status, submitted := runForceCheckWorkerTest(t, false)
	if status != "unchanged" {
		t.Fatalf("status = %q, want unchanged", status)
	}
	if submitted {
		t.Fatal("expected SubmitMagnet not to be called")
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

func (m *failingSubmitManager) SubmitMagnet(context.Context, string, model.ManagerAddFileOptions) error {
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

// barrierTracker holds every Response until two of them are in flight at once.
// With a per-tracker lock the two workers meet at the barrier; with one global
// lock the second never gets in and the test times out.
type barrierTracker struct {
	safeFakeTracker
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
	// без записи истории force_check остаётся взведённым, а запись каждый прогон
	// снова считается обновлённой и снова упирается в already added
	if len(store.saved) != 1 {
		t.Fatalf("saved = %d, want 1", len(store.saved))
	}
	if store.saved[0].RecheckAt == nil || !store.saved[0].RecheckAt.After(time.Now()) {
		t.Fatalf("recheck_at = %v, want a future time", store.saved[0].RecheckAt)
	}
}
