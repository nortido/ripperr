package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
	"ripperr/internal/model/tracker"
	storagePkg "ripperr/internal/storage"
	"ripperr/internal/tracker/client"
)

const (
	relURL   = "https://example.com/t/1"
	otherURL = "https://example.com/t/2"
)

type releaseManager struct {
	fakeManager
	lists     [][]*model.Entry
	listCalls int
	partial   bool
	removed   [][]model.ManagerDeleteOptions
	accepted  []string
	removeErr error
	log       *[]string
	onList    func(call int)
}

func (m *releaseManager) GetEntries(context.Context, model.FilterOptions) ([]*model.Entry, error) {
	call := m.listCalls
	m.listCalls++
	if m.onList != nil {
		m.onList(call)
	}
	var out []*model.Entry
	if len(m.lists) > 0 {
		out = m.lists[min(call, len(m.lists)-1)]
	}
	if m.partial {
		return out, fmt.Errorf("%w: test", model.ErrPartialList)
	}
	return out, nil
}

func (m *releaseManager) Remove(_ context.Context, targets []model.ManagerDeleteOptions) ([]string, error) {
	m.removed = append(m.removed, targets)
	if m.log != nil {
		*m.log = append(*m.log, "remove")
	}
	if m.accepted != nil || m.removeErr != nil {
		return m.accepted, m.removeErr
	}
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, t.Hash)
	}
	return out, nil
}

type releaseStorage struct {
	*fakeStorage
	log *[]string
}

func (s releaseStorage) DeleteItem(ctx context.Context, urlHash string) error {
	*s.log = append(*s.log, "delete-item")
	return s.fakeStorage.DeleteItem(ctx, urlHash)
}

func (s releaseStorage) DeleteIgnore(ctx context.Context, hash string) error {
	*s.log = append(*s.log, "delete-ignore")
	return s.fakeStorage.DeleteIgnore(ctx, hash)
}

func relEntry(hash, url, savePath, name string) *model.Entry {
	return &model.Entry{Hash: hash, URL: url, SavePath: savePath, Name: name, Category: "tv"}
}

func newRemoveHandler(mgr *releaseManager) (*handler, *fakeStorage, *[]string) {
	store := &fakeStorage{}
	log := &[]string{}
	mgr.log = log
	return &handler{manager: mgr, storage: releaseStorage{fakeStorage: store, log: log}, state: NewState(), trackerService: &adoptTracker{}}, store, log
}

func removedHashes(mgr *releaseManager) []string {
	var out []string
	for _, call := range mgr.removed {
		for _, t := range call {
			out = append(out, t.Hash)
		}
	}
	return out
}

func TestRemoveReleaseRemovesAllTargetsAndRow(t *testing.T) {
	mgr := &releaseManager{lists: [][]*model.Entry{{
		relEntry("x", relURL, "/data/tv", "Show"),
		relEntry("y", relURL, "/data/tv2", "Show"),
		relEntry("z", otherURL, "/data/tv", "Other"),
	}}}
	h, store, log := newRemoveHandler(mgr)
	key := storagePkg.URLHash(relURL)
	if err := h.removeRelease(context.Background(), RemoveRequest{URLHash: key, Files: false, Torrents: 2}); err != nil {
		t.Fatal(err)
	}
	if len(mgr.removed) != 1 || !slices.Equal(removedHashes(mgr), []string{"x", "y"}) {
		t.Fatalf("Remove got %v, want one call with [x y]", mgr.removed)
	}
	for _, o := range mgr.removed[0] {
		if o.DeleteFiles || o.Category != "tv" || o.Name != "Show" {
			t.Fatalf("delete opts %+v, want category tv, name Show, no files", o)
		}
	}
	if !slices.Equal(store.deleted, []string{key}) || !slices.Equal(store.unignored, []string{key}) {
		t.Fatalf("DeleteItem %v DeleteIgnore %v, want [%s]", store.deleted, store.unignored, key)
	}
	if !slices.Equal(*log, []string{"remove", "delete-item", "delete-ignore"}) {
		t.Fatalf("order %v, want DB after the client", *log)
	}
	if !h.state.Removed("x") || !h.state.Removed("y") || h.state.Removed("z") {
		t.Fatal("marks: want x and y removed, z untouched")
	}
	if unlock, ok := h.state.TryLockTorrent("x"); !ok {
		t.Fatal("x stays locked")
	} else {
		unlock()
	}
}

func TestRemoveReleaseCountMismatch(t *testing.T) {
	mgr := &releaseManager{lists: [][]*model.Entry{{
		relEntry("x", relURL, "/data/tv", "Show"),
		relEntry("y", relURL, "/data/tv", "Show"),
	}}}
	h, store, _ := newRemoveHandler(mgr)
	err := h.removeRelease(context.Background(), RemoveRequest{URLHash: storagePkg.URLHash(relURL), Torrents: 1})
	if !errors.Is(err, ErrTorrentsChanged) {
		t.Fatalf("err = %v, want ErrTorrentsChanged", err)
	}
	if len(mgr.removed) != 0 || len(store.deleted) != 0 || len(store.unignored) != 0 {
		t.Fatalf("removed %v deleted %v unignored %v, want nothing", mgr.removed, store.deleted, store.unignored)
	}
}

func TestRemoveReleaseBusy(t *testing.T) {
	mgr := &releaseManager{lists: [][]*model.Entry{{
		relEntry("x", relURL, "/data/tv", "Show"),
		relEntry("y", relURL, "/data/tv", "Show"),
	}}}
	h, store, _ := newRemoveHandler(mgr)
	unlockY, ok := h.state.TryLockTorrent("y")
	if !ok {
		t.Fatal("lock y")
	}
	defer unlockY()
	err := h.removeRelease(context.Background(), RemoveRequest{URLHash: storagePkg.URLHash(relURL), Torrents: 2})
	if !errors.Is(err, ErrReleaseBusy) {
		t.Fatalf("err = %v, want ErrReleaseBusy", err)
	}
	if len(mgr.removed) != 0 || len(store.deleted) != 0 || h.state.Removed("x") {
		t.Fatal("busy remove changed something")
	}
	unlockX, ok := h.state.TryLockTorrent("x")
	if !ok {
		t.Fatal("x stays locked after a busy remove")
	}
	unlockX()
}

func TestRemoveReleaseRelistLostTarget(t *testing.T) {
	x := relEntry("x", relURL, "/data/tv", "Show")
	mgr := &releaseManager{lists: [][]*model.Entry{{x, relEntry("y", relURL, "/data/tv", "Show")}, {x}}}
	h, store, _ := newRemoveHandler(mgr)
	err := h.removeRelease(context.Background(), RemoveRequest{URLHash: storagePkg.URLHash(relURL), Torrents: 2})
	if !errors.Is(err, ErrTorrentsChanged) {
		t.Fatalf("err = %v, want ErrTorrentsChanged", err)
	}
	if mgr.listCalls != 2 || len(mgr.removed) != 0 || len(store.deleted) != 0 || len(store.unignored) != 0 {
		t.Fatalf("lists %d removed %v deleted %v unignored %v, want 2 lists and nothing removed", mgr.listCalls, mgr.removed, store.deleted, store.unignored)
	}
}

func TestRemoveReleaseRelistChanged(t *testing.T) {
	x := relEntry("x", relURL, "/data/tv", "Show")
	mgr := &releaseManager{lists: [][]*model.Entry{{x}, {x, relEntry("y2", relURL, "/data/tv", "Show")}}}
	h, store, _ := newRemoveHandler(mgr)
	err := h.removeRelease(context.Background(), RemoveRequest{URLHash: storagePkg.URLHash(relURL), Torrents: 1})
	if !errors.Is(err, ErrTorrentsChanged) {
		t.Fatalf("err = %v, want ErrTorrentsChanged", err)
	}
	if mgr.listCalls != 2 || len(mgr.removed) != 0 || len(store.deleted) != 0 {
		t.Fatalf("lists %d removed %v deleted %v, want 2 lists and nothing removed", mgr.listCalls, mgr.removed, store.deleted)
	}
}

func TestRemoveReleaseFilesRefused(t *testing.T) {
	cases := []struct {
		name    string
		targets []*model.Entry
		others  []*model.Entry
		ok      bool
	}{
		{
			name:    "two targets in different folders",
			targets: []*model.Entry{relEntry("x", relURL, "/data/tv", "Show"), relEntry("y", relURL, "/data/tv2", "Show")},
			ok:      true,
		},
		{
			name:    "same root",
			targets: []*model.Entry{relEntry("x", relURL, "/data/tv/", "show s01")},
			others:  []*model.Entry{relEntry("w", otherURL, "/data/tv", "Show S01")},
		},
		{
			name:    "other nested under target",
			targets: []*model.Entry{relEntry("x", relURL, "/data/tv", "Show")},
			others:  []*model.Entry{relEntry("w", otherURL, "/data/tv/Show", "S01")},
		},
		{
			name:    "target nested under other",
			targets: []*model.Entry{relEntry("x", relURL, "/data/tv/Show", "S01")},
			others:  []*model.Entry{relEntry("w", otherURL, "/data/tv", "Show")},
		},
		{
			name:    "empty save path",
			targets: []*model.Entry{relEntry("x", relURL, "", "Show")},
		},
		{
			name:    "backslash other nested under target",
			targets: []*model.Entry{relEntry("x", relURL, `D:\Downloads`, "Show")},
			others:  []*model.Entry{relEntry("w", otherURL, `D:\Downloads\Show`, "S01")},
		},
		{
			name:    "backslash target nested under other",
			targets: []*model.Entry{relEntry("x", relURL, `D:\Downloads\Show`, "S01")},
			others:  []*model.Entry{relEntry("w", otherURL, `d:\downloads`, "show")},
		},
		{
			name:    "mixed separators",
			targets: []*model.Entry{relEntry("x", relURL, "D:/Downloads", "Show")},
			others:  []*model.Entry{relEntry("w", otherURL, `D:\Downloads\Show`, "S01")},
		},
		{
			name:    "other without save path",
			targets: []*model.Entry{relEntry("x", relURL, "/data/tv", "Show")},
			others:  []*model.Entry{relEntry("w", otherURL, "", "Other")},
		},
		{
			name:    "renamed other shares content path",
			targets: []*model.Entry{pathEntry(relEntry("x", relURL, "/data/tv", "Show"), "/data/tv/Show S01")},
			others:  []*model.Entry{pathEntry(relEntry("w", otherURL, "/data/tv", "Show S01 renamed"), "/data/tv/Show S01")},
		},
		{
			name:    "content path locates entries without save path",
			targets: []*model.Entry{pathEntry(relEntry("x", relURL, "", "Show"), "/data/tv/Show")},
			others:  []*model.Entry{pathEntry(relEntry("w", otherURL, "", "Show"), "/data/movies/Film")},
			ok:      true,
		},
		{
			name:    "backslash similar name is not nested",
			targets: []*model.Entry{relEntry("x", relURL, `D:\Downloads`, "Show")},
			others:  []*model.Entry{relEntry("w", otherURL, `D:\Downloads`, "Show2")},
			ok:      true,
		},
		{
			name:    "similar name is not nested",
			targets: []*model.Entry{relEntry("x", relURL, "/data/tv", "Show")},
			others:  []*model.Entry{relEntry("w", otherURL, "/data/tv", "Show2")},
			ok:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &releaseManager{lists: [][]*model.Entry{append(slices.Clone(tc.targets), tc.others...)}}
			h, store, _ := newRemoveHandler(mgr)
			err := h.removeRelease(context.Background(), RemoveRequest{URLHash: storagePkg.URLHash(relURL), Files: true, Torrents: len(tc.targets)})
			if tc.ok {
				if err != nil {
					t.Fatal(err)
				}
				if len(mgr.removed) != 1 || !mgr.removed[0][0].DeleteFiles {
					t.Fatalf("Remove got %v, want one call with files", mgr.removed)
				}
				return
			}
			if !errors.Is(err, model.ErrFilesNotAllowed) {
				t.Fatalf("err = %v, want ErrFilesNotAllowed", err)
			}
			if len(mgr.removed) != 0 || len(store.deleted) != 0 {
				t.Fatalf("removed %v deleted %v, want nothing", mgr.removed, store.deleted)
			}
		})
	}
}

func TestRemoveReleaseZeroTargets(t *testing.T) {
	mgr := &releaseManager{lists: [][]*model.Entry{{relEntry("z", otherURL, "/data/tv", "Other")}}}
	h, store, _ := newRemoveHandler(mgr)
	key := storagePkg.URLHash(relURL)
	if err := h.removeRelease(context.Background(), RemoveRequest{URLHash: key, Files: true, Torrents: 0}); err != nil {
		t.Fatal(err)
	}
	if len(mgr.removed) != 0 {
		t.Fatalf("Remove got %v, want no client call", mgr.removed)
	}
	if !slices.Equal(store.deleted, []string{key}) || !slices.Equal(store.unignored, []string{key}) {
		t.Fatalf("DeleteItem %v DeleteIgnore %v, want [%s]", store.deleted, store.unignored, key)
	}
}

func TestRemoveReleasePartialList(t *testing.T) {
	mgr := &releaseManager{lists: [][]*model.Entry{{relEntry("x", relURL, "/data/tv", "Show")}}, partial: true}
	h, store, _ := newRemoveHandler(mgr)
	key := storagePkg.URLHash(relURL)
	if err := h.removeRelease(context.Background(), RemoveRequest{URLHash: key, Torrents: 1}); !errors.Is(err, model.ErrPartialList) {
		t.Fatalf("remove err = %v, want ErrPartialList", err)
	}
	if _, err := h.removalInfo(context.Background(), key); !errors.Is(err, model.ErrPartialList) {
		t.Fatalf("removal err = %v, want ErrPartialList", err)
	}
	if len(mgr.removed) != 0 || len(store.deleted) != 0 || len(store.unignored) != 0 {
		t.Fatal("partial list changed something")
	}
}

func TestRemoveReleaseUnconfirmedKeepsRow(t *testing.T) {
	mgr := &releaseManager{
		lists:     [][]*model.Entry{{relEntry("x", relURL, "/data/tv", "Show")}},
		accepted:  []string{"x"},
		removeErr: fmt.Errorf("%w: x", model.ErrRemoveUnconfirmed),
	}
	h, store, _ := newRemoveHandler(mgr)
	err := h.removeRelease(context.Background(), RemoveRequest{URLHash: storagePkg.URLHash(relURL), Torrents: 1})
	if !errors.Is(err, model.ErrRemoveUnconfirmed) {
		t.Fatalf("err = %v, want ErrRemoveUnconfirmed", err)
	}
	if !h.state.Removed("x") {
		t.Fatal("accepted x is not marked")
	}
	if len(store.deleted) != 0 || len(store.unignored) != 0 {
		t.Fatalf("deleted %v unignored %v, want the row kept", store.deleted, store.unignored)
	}
}

func TestRemovalInfo(t *testing.T) {
	z := relEntry("z", otherURL, "/data/movies", "Film")
	cases := []struct {
		name string
		list []*model.Entry
		want RemovalInfo
	}{
		{name: "gone", list: []*model.Entry{z}, want: RemovalInfo{Torrents: 0, Files: false}},
		{name: "one", list: []*model.Entry{relEntry("x", relURL, "/data/tv", "Show"), z}, want: RemovalInfo{Torrents: 1, Files: true}},
		{name: "two", list: []*model.Entry{relEntry("x", relURL, "/data/tv", "Show"), relEntry("y", relURL, "/data/tv", "Show"), z}, want: RemovalInfo{Torrents: 2, Files: true}},
		{name: "two targets alone", list: []*model.Entry{relEntry("x", relURL, "/data/tv", "Show"), relEntry("y", relURL, "/data/tv", "Show")}, want: RemovalInfo{Torrents: 2, Files: true}},
		{name: "shared root", list: []*model.Entry{relEntry("x", relURL, "/data/tv", "Show"), relEntry("w", otherURL, "/data/tv", "show")}, want: RemovalInfo{Torrents: 1, Files: false}},
		{name: "no url is other", list: []*model.Entry{relEntry("x", relURL, "/data/tv", "Show"), relEntry("w", "", "/data/tv/Show", "")}, want: RemovalInfo{Torrents: 1, Files: false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &releaseManager{lists: [][]*model.Entry{tc.list}}
			h, _, _ := newRemoveHandler(mgr)
			got, err := h.removalInfo(context.Background(), storagePkg.URLHash(relURL))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if len(mgr.removed) != 0 {
				t.Fatal("preview called Remove")
			}
		})
	}
}

type countingPageTracker struct {
	fixedDatePageTracker
	calls   int
	fetched chan struct{}
}

func (p *countingPageTracker) Response(ctx context.Context, ttid int, link string, download bool) (*bytes.Buffer, error) {
	p.calls++
	if p.fetched != nil && p.calls == 1 {
		close(p.fetched)
	}
	return p.fixedDatePageTracker.Response(ctx, ttid, link, download)
}

func removedWorkerHandler(store *loggingStorage, mgr manager, tr trackerService) *handler {
	return &handler{
		config:         model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage:        store,
		manager:        mgr,
		trackerService: tr,
		state:          NewState(),
		mx:             helper.KeyedMutex{},
	}
}

func changedPageTracker() *countingPageTracker {
	page := bytes.NewBufferString(strings.Repeat("x", 60))
	return &countingPageTracker{fixedDatePageTracker: fixedDatePageTracker{
		pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: page},
		date:        time.Now(),
	}}
}

func TestWorkerSkipsRemovedTorrent(t *testing.T) {
	store := &loggingStorage{}
	mgr := &submitTrackingManager{}
	tr := changedPageTracker()
	h := removedWorkerHandler(store, mgr, tr)
	old := time.Now().Add(-48 * time.Hour)
	e := &model.Entry{Hash: "ab", URL: relURL, Name: "test", URLHash: "abc", Timestamp: &old}
	h.state.MarkRemoved("AB")
	if err := h.worker(context.Background(), 1, e, nil); err != nil {
		t.Fatal(err)
	}
	if tr.calls != 0 || mgr.submitted || len(store.logs) != 0 {
		t.Fatalf("page requests %d, submitted %v, check rows %d, want none", tr.calls, mgr.submitted, len(store.logs))
	}
}

func TestSubmitLockedSkipsTorrentRemovedWhileWaiting(t *testing.T) {
	store := &loggingStorage{}
	mgr := &submitTrackingManager{}
	tr := changedPageTracker()
	tr.fetched = make(chan struct{})
	h := removedWorkerHandler(store, mgr, tr)
	old := time.Now().Add(-48 * time.Hour)
	e := &model.Entry{Hash: "ab", URL: relURL, Name: "test", URLHash: "abc", Timestamp: &old}
	unlock := h.state.LockTorrent("ab")
	done := make(chan error, 1)
	go func() { done <- h.worker(context.Background(), 1, e, nil) }()
	select {
	case <-tr.fetched:
	case err := <-done:
		unlock()
		t.Fatalf("worker ended before the page request: %v", err)
	case <-time.After(5 * time.Second):
		unlock()
		t.Fatal("worker did not request the page")
	}
	h.state.MarkRemoved("ab")
	unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not finish")
	}
	if mgr.submitted || len(store.logs) != 0 || len(store.saved) != 0 {
		t.Fatalf("submitted %v, check rows %d, saved %d, want none", mgr.submitted, len(store.logs), len(store.saved))
	}
}

func TestLogCheckSkipsRemovedTorrent(t *testing.T) {
	store := &loggingStorage{}
	h := removedWorkerHandler(store, &fakeManager{}, &fakeTracker{id: 1})
	e := &model.Entry{Hash: "ab", URL: relURL, Name: "test", URLHash: "abc"}
	h.state.MarkRemoved("ab")
	h.logCheck(context.Background(), e, 1, "updated", "", nil)
	if len(store.logs) != 0 {
		t.Fatalf("check rows %d, want 0", len(store.logs))
	}
	h.logCheck(context.Background(), &model.Entry{Hash: "cd", URL: otherURL, Name: "kept", URLHash: "def"}, 1, "updated", "", nil)
	if len(store.logs) != 1 {
		t.Fatalf("check rows %d, want 1 for an unmarked torrent", len(store.logs))
	}
}

func TestCollectEntriesClearsRemovedOnRelist(t *testing.T) {
	state := NewState()
	state.MarkRemoved("a")
	time.Sleep(2 * time.Millisecond)
	mgr := &releaseManager{
		lists:  [][]*model.Entry{{relEntry("a", relURL, "/data/tv", "A"), relEntry("b", otherURL, "/data/tv", "B")}},
		onList: func(int) { state.MarkRemoved("b") },
	}
	h := &handler{manager: mgr, storage: &fakeStorage{}, trackerService: &fakeTracker{id: 1}, state: state}
	if _, _, _, _, err := h.collectEntries(context.Background()); err != nil {
		t.Fatal(err)
	}
	if state.Removed("a") {
		t.Fatal("mark set before the listing survived a listing that has the torrent")
	}
	if !state.Removed("b") {
		t.Fatal("mark set during the listing was cleared")
	}
}

func TestRunRegistersReleaseActions(t *testing.T) {
	store := &fakeStorage{}
	h, tr := newPollHandler(t, store, &pollManager{list: []*model.Entry{
		pollEntry("h1", "Show S01", "tv", "https://example.com/t/1", time.Now()),
	}}, pollConfig())
	h.state = NewState()
	tr.ids[""] = 0
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		info, err := h.state.ReleaseRemoval(context.Background(), "1")
		if err == nil {
			if info != (RemovalInfo{}) {
				t.Fatalf("removal %+v, want zero targets", info)
			}
			break
		}
		if !errors.Is(err, ErrPollUnavailable) || time.Now().After(deadline) {
			t.Fatalf("ReleaseRemoval error %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := h.state.RemoveRelease(context.Background(), RemoveRequest{URLHash: "1", Torrents: 1}); !errors.Is(err, ErrTorrentsChanged) {
		t.Fatalf("RemoveRelease = %v, want ErrTorrentsChanged", err)
	}
	if _, err := h.state.AddRelease(context.Background(), AddRequest{}); !errors.Is(err, ErrUnknownTracker) {
		t.Fatalf("AddRelease = %v, want ErrUnknownTracker from the registered Add", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
	if _, err := h.state.AddRelease(context.Background(), AddRequest{}); !errors.Is(err, ErrPollUnavailable) {
		t.Fatalf("AddRelease after Run = %v, want ErrPollUnavailable", err)
	}
	if _, err := h.state.ReleaseRemoval(context.Background(), "1"); !errors.Is(err, ErrPollUnavailable) {
		t.Fatalf("ReleaseRemoval after Run = %v, want ErrPollUnavailable", err)
	}
	if err := h.state.RemoveRelease(context.Background(), RemoveRequest{URLHash: "1"}); !errors.Is(err, ErrPollUnavailable) {
		t.Fatalf("RemoveRelease after Run = %v, want ErrPollUnavailable", err)
	}
}

const (
	addTopic     = "https://mirror.example/forum/viewtopic.php?t=1"
	addCanonical = "https://tr.example/forum/viewtopic.php?t=1"
)

type addClient struct {
	mockPageClient
	primary, id, magnet string
}

func (c *addClient) PrimaryDomain() string { return c.primary }
func (c *addClient) PickTorrent(*bytes.Buffer, bool, tracker.Held) tracker.Pick {
	return tracker.Pick{DownloadID: c.id}
}
func (c *addClient) ParseMagnet(*bytes.Buffer, bool) string { return c.magnet }
func (c *addClient) DownloadLink(id string) string          { return "https://tr.example/dl?id=" + id }

type addCall struct {
	link     string
	download bool
}

type addTracker struct {
	fakeTracker
	hosts   map[string]int
	client  *addClient
	torrent []byte
	dlErr   error
	calls   []addCall
}

func (t *addTracker) GetID(link string) int {
	u, err := url.Parse(link)
	if err != nil {
		return 0
	}
	return t.hosts[u.Host]
}

func (t *addTracker) GetCachedClient(context.Context, int) (client.IClient, error) {
	return t.client, nil
}

func (t *addTracker) Response(_ context.Context, _ int, link string, download bool) (*bytes.Buffer, error) {
	t.calls = append(t.calls, addCall{link: link, download: download})
	if !download {
		return bytes.NewBufferString(strings.Repeat("x", 60)), nil
	}
	if t.dlErr != nil {
		return nil, t.dlErr
	}
	return bytes.NewBuffer(t.torrent), nil
}

type addedTorrent struct {
	data []byte
	opts model.ManagerAddFileOptions
}

type addManager struct {
	fakeManager
	added     []addedTorrent
	addErr    error
	list      []*model.Entry
	listCalls int
}

func (m *addManager) Add(_ context.Context, data []byte, opts model.ManagerAddFileOptions) error {
	if m.addErr != nil {
		return m.addErr
	}
	m.added = append(m.added, addedTorrent{data: data, opts: opts})
	hash, _ := torrentmeta.InfoHash(data)
	comment, _ := torrentmeta.Comment(data)
	m.list = append(m.list, &model.Entry{Hash: hash, Name: "Show S01", Category: opts.Category, Tag: opts.Tag, URL: comment})
	return nil
}

func (m *addManager) GetEntries(context.Context, model.FilterOptions) ([]*model.Entry, error) {
	m.listCalls++
	return m.list, nil
}

func newAddHandler(t *testing.T) (*handler, *addTracker, *addManager, *fakeStorage) {
	t.Helper()
	tr := &addTracker{
		hosts:   map[string]int{"tr.example": 1, "mirror.example": 1},
		client:  &addClient{primary: "tr.example", id: "42"},
		torrent: buildTestTorrentBytes(t, ""),
	}
	mgr := &addManager{}
	store := &fakeStorage{}
	cfg := pollConfig()
	cfg.MagnetTimeout = 5
	return &handler{config: cfg, manager: mgr, storage: store, trackerService: tr, state: NewState()}, tr, mgr, store
}

func addedComment(t *testing.T, mgr *addManager) string {
	t.Helper()
	if len(mgr.added) != 1 {
		t.Fatalf("Add called %d times, want 1", len(mgr.added))
	}
	comment, err := torrentmeta.Comment(mgr.added[0].data)
	if err != nil {
		t.Fatal(err)
	}
	return comment
}

func TestAddReleaseDownloadsWithComment(t *testing.T) {
	h, tr, mgr, store := newAddHandler(t)
	res, err := h.addRelease(context.Background(), AddRequest{URL: " " + addTopic + " ", Category: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Unignored {
		t.Fatal("Unignored true, want false")
	}
	if len(tr.calls) != 2 || tr.calls[0] != (addCall{addCanonical, false}) || tr.calls[1] != (addCall{"https://tr.example/dl?id=42", true}) {
		t.Fatalf("tracker calls %+v, want page and download on tr.example", tr.calls)
	}
	if got := addedComment(t, mgr); got != addCanonical {
		t.Fatalf("comment %q, want %q", got, addCanonical)
	}
	if want := (model.ManagerAddFileOptions{Category: "tv", AutoManaged: true, Comment: addCanonical}); !reflect.DeepEqual(mgr.added[0].opts, want) {
		t.Fatalf("opts %+v, want %+v", mgr.added[0].opts, want)
	}
	if len(store.newAdds) != 1 || store.newAdds[0].entry.URLHash != storagePkg.URLHash(addCanonical) {
		t.Fatalf("poll rows %+v, want one for %s", store.newAdds, addCanonical)
	}
	if len(store.unignored) != 0 || len(store.streakReset) != 0 {
		t.Fatalf("ignore touched: %v %v", store.unignored, store.streakReset)
	}
	hash, _ := torrentmeta.InfoHash(tr.torrent)
	if unlock, ok := h.state.TryLockTorrent(hash); !ok {
		t.Fatal("torrent stays locked")
	} else {
		unlock()
	}
}

func TestAddReleaseTagsMode(t *testing.T) {
	h, _, mgr, _ := newAddHandler(t)
	h.config.Categories = nil
	h.config.WatchTags = []string{"watch"}
	if _, err := h.addRelease(context.Background(), AddRequest{URL: addTopic, Tag: "watch"}); err != nil {
		t.Fatal(err)
	}
	if want := (model.ManagerAddFileOptions{Tag: "watch", Comment: addCanonical}); len(mgr.added) != 1 || !reflect.DeepEqual(mgr.added[0].opts, want) {
		t.Fatalf("added %+v, want opts %+v", mgr.added, want)
	}
}

func TestAddReleaseRejectsBeforeFetch(t *testing.T) {
	cases := []struct {
		name string
		link string
		req  AddRequest
		tags []string
		want error
	}{
		{"unknown tracker", "https://nope.example/t/1", AddRequest{Category: "tv"}, nil, ErrUnknownTracker},
		{"category not watched", addTopic, AddRequest{Category: "movies"}, nil, ErrNotWatched},
		{"tag in categories mode", addTopic, AddRequest{Category: "tv", Tag: "watch"}, nil, ErrNotWatched},
		{"category in tags mode", addTopic, AddRequest{Category: "tv"}, []string{"watch"}, ErrNotWatched},
		{"tag not watched", addTopic, AddRequest{Tag: "other"}, []string{"watch"}, ErrNotWatched},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, tr, mgr, store := newAddHandler(t)
			if c.tags != nil {
				h.config.Categories = nil
				h.config.WatchTags = c.tags
			}
			c.req.URL = c.link
			if _, err := h.addRelease(context.Background(), c.req); !errors.Is(err, c.want) {
				t.Fatalf("error %v, want %v", err, c.want)
			}
			if len(tr.calls) != 0 || len(mgr.added) != 0 || len(store.newAdds) != 0 {
				t.Fatalf("calls %+v added %d rows %d, want none", tr.calls, len(mgr.added), len(store.newAdds))
			}
		})
	}
}

func TestAddReleaseMagnetFallback(t *testing.T) {
	const magnet = "magnet:?xt=urn:btih:00"
	fetchOK := func(t *testing.T) func(context.Context, string) ([]byte, error) {
		return func(_ context.Context, m string) ([]byte, error) {
			if m != magnet {
				t.Fatalf("magnet %q, want %q", m, magnet)
			}
			return buildTestTorrentBytes(t, "raw"), nil
		}
	}
	t.Run("no download id", func(t *testing.T) {
		h, tr, mgr, _ := newAddHandler(t)
		tr.client.id, tr.client.magnet = "", magnet
		h.magnetFetch = fetchOK(t)
		if _, err := h.addRelease(context.Background(), AddRequest{URL: addTopic, Category: "tv"}); err != nil {
			t.Fatal(err)
		}
		if got := addedComment(t, mgr); got != addCanonical {
			t.Fatalf("comment %q, want %q", got, addCanonical)
		}
		if len(tr.calls) != 1 || tr.calls[0].download {
			t.Fatalf("tracker calls %+v, want the page only", tr.calls)
		}
	})
	t.Run("download failed", func(t *testing.T) {
		h, tr, mgr, _ := newAddHandler(t)
		tr.client.magnet = magnet
		tr.dlErr = errors.New("dl boom")
		h.magnetFetch = fetchOK(t)
		if _, err := h.addRelease(context.Background(), AddRequest{URL: addTopic, Category: "tv"}); err != nil {
			t.Fatal(err)
		}
		if got := addedComment(t, mgr); got != addCanonical {
			t.Fatalf("comment %q, want %q", got, addCanonical)
		}
	})
	t.Run("both failed", func(t *testing.T) {
		h, tr, mgr, _ := newAddHandler(t)
		tr.client.magnet = magnet
		tr.dlErr = errors.New("dl boom")
		h.magnetFetch = func(context.Context, string) ([]byte, error) { return nil, errors.New("magnet boom") }
		_, err := h.addRelease(context.Background(), AddRequest{URL: addTopic, Category: "tv"})
		if err == nil || !strings.Contains(err.Error(), "dl boom") || !strings.Contains(err.Error(), "magnet boom") {
			t.Fatalf("error %v, want both causes", err)
		}
		if len(mgr.added) != 0 {
			t.Fatal("Add called")
		}
	})
}

func TestAddReleaseNoTorrent(t *testing.T) {
	h, tr, mgr, _ := newAddHandler(t)
	tr.client.id = ""
	if _, err := h.addRelease(context.Background(), AddRequest{URL: addTopic, Category: "tv"}); !errors.Is(err, ErrNoTorrent) {
		t.Fatalf("error %v, want ErrNoTorrent", err)
	}
	if len(mgr.added) != 0 {
		t.Fatal("Add called")
	}
}

func TestAddReleaseTrackerComment(t *testing.T) {
	own := "https://tr.example/forum/viewtopic.php?t=9"
	cases := []struct{ name, comment, want string }{
		{"own tracker comment kept", own, own},
		{"foreign comment replaced", "https://other.example/x", addCanonical},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, tr, mgr, store := newAddHandler(t)
			tr.torrent = buildTestTorrentBytes(t, c.comment)
			if _, err := h.addRelease(context.Background(), AddRequest{URL: addTopic, Category: "tv"}); err != nil {
				t.Fatal(err)
			}
			if got := addedComment(t, mgr); got != c.want || mgr.added[0].opts.Comment != c.want {
				t.Fatalf("comment %q opts %q, want %q", got, mgr.added[0].opts.Comment, c.want)
			}
			if len(store.newAdds) != 1 || store.newAdds[0].entry.URLHash != storagePkg.URLHash(c.want) {
				t.Fatalf("poll rows %+v, want one for %s", store.newAdds, c.want)
			}
		})
	}
}

func TestAddReleaseAlreadyAdded(t *testing.T) {
	h, _, mgr, store := newAddHandler(t)
	mgr.addErr = fmt.Errorf("add: %w", model.ErrAlreadyAdded)
	store.ignore = map[string]struct{}{storagePkg.URLHash(addCanonical): {}}
	if _, err := h.addRelease(context.Background(), AddRequest{URL: addTopic, Category: "tv"}); !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("error %v, want ErrAlreadyAdded", err)
	}
	if len(store.unignored) != 0 || len(store.streakReset) != 0 || mgr.listCalls != 0 {
		t.Fatalf("unignored %v reset %v lists %d, want none", store.unignored, store.streakReset, mgr.listCalls)
	}
}

func TestAddReleaseUnignoresAndClearsMark(t *testing.T) {
	h, tr, _, store := newAddHandler(t)
	key := storagePkg.URLHash(addCanonical)
	store.ignore = map[string]struct{}{key: {}}
	hash, _ := torrentmeta.InfoHash(tr.torrent)
	h.state.MarkRemoved(hash)
	res, err := h.addRelease(context.Background(), AddRequest{URL: addTopic, Category: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unignored || !slices.Equal(store.unignored, []string{key}) || !slices.Equal(store.streakReset, []string{key}) {
		t.Fatalf("result %+v unignored %v reset %v, want true and [%s]", res, store.unignored, store.streakReset, key)
	}
	if h.state.Removed(hash) {
		t.Fatal("removal mark stays")
	}
}

func TestAddReleaseBusy(t *testing.T) {
	h, tr, mgr, _ := newAddHandler(t)
	hash, _ := torrentmeta.InfoHash(tr.torrent)
	unlock, ok := h.state.TryLockTorrent(hash)
	if !ok {
		t.Fatal("lock")
	}
	defer unlock()
	if _, err := h.addRelease(context.Background(), AddRequest{URL: addTopic, Category: "tv"}); !errors.Is(err, ErrReleaseBusy) {
		t.Fatalf("error %v, want ErrReleaseBusy", err)
	}
	if len(mgr.added) != 0 {
		t.Fatal("Add called")
	}
}

func pathEntry(e *model.Entry, contentPath string) *model.Entry {
	e.ContentPath = contentPath
	return e
}

func TestRemoveReleaseAdoptsCommentless(t *testing.T) {
	mgr := &releaseManager{lists: [][]*model.Entry{{relEntry("x", "", "/data/tv", "Show"), relEntry("z", otherURL, "/data/movies", "Film")}}}
	h, store, _ := newRemoveHandler(mgr)
	h.trackerService = &adoptTracker{url: relURL}
	key := storagePkg.URLHash(relURL)
	got, err := h.removalInfo(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if got.Torrents != 1 {
		t.Fatalf("preview torrents = %d, want 1 adopted", got.Torrents)
	}
	if err = h.removeRelease(context.Background(), RemoveRequest{URLHash: key, Torrents: 1}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(removedHashes(mgr), []string{"x"}) || !slices.Equal(store.deleted, []string{key}) {
		t.Fatalf("removed %v deleted %v, want x through the client and the row", removedHashes(mgr), store.deleted)
	}
}

func TestRemoveReleaseAdoptErrorKeepsRow(t *testing.T) {
	mgr := &releaseManager{lists: [][]*model.Entry{{relEntry("x", "", "/data/tv", "Show")}}}
	h, store, _ := newRemoveHandler(mgr)
	h.trackerService = &adoptTracker{err: errors.New("lookup timeout")}
	key := storagePkg.URLHash(relURL)
	err := h.removeRelease(context.Background(), RemoveRequest{URLHash: key, Torrents: 0})
	if !errors.Is(err, model.ErrPartialList) {
		t.Fatalf("err = %v, want ErrPartialList", err)
	}
	if len(mgr.removed) != 0 || len(store.deleted) != 0 || len(store.unignored) != 0 {
		t.Fatalf("removed %v deleted %v unignored %v, want nothing", mgr.removed, store.deleted, store.unignored)
	}
}

func TestSaveHistorySkipsRemoved(t *testing.T) {
	store := &loggingStorage{}
	h := removedWorkerHandler(store, &submitTrackingManager{}, changedPageTracker())
	h.state.MarkRemoved("AB")
	now := time.Now()
	e := &model.Entry{Hash: "ab", URL: relURL, Name: "test", URLHash: "abc"}
	if err := h.saveHistory(context.Background(), 1, e, &now, false); err != nil {
		t.Fatal(err)
	}
	if len(store.saved) != 0 {
		t.Fatalf("saved %+v, want no row for a removed torrent", store.saved)
	}
}
