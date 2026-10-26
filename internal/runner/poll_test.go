package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"ripperr/internal/model"
	runnerModel "ripperr/internal/model/runner"
	storageModel "ripperr/internal/model/storage"
	storagePkg "ripperr/internal/storage"
)

type pollStorage struct {
	*fakeStorage
	t *testing.T
}

func (s pollStorage) SaveItem(context.Context, *runnerModel.HistoryItem) error {
	s.t.Error("poll called SaveItem")
	return nil
}

func (s pollStorage) AddCheckLog(context.Context, *storageModel.CheckLogEntry) (int, error) {
	s.t.Error("poll called AddCheckLog")
	return 0, nil
}

func (s pollStorage) DeleteItem(context.Context, string) error {
	s.t.Error("poll called DeleteItem")
	return nil
}

func (s pollStorage) DeleteNewRelease(ctx context.Context, urlHash string) error {
	return s.fakeStorage.DeleteNewRelease(ctx, urlHash)
}

func (s pollStorage) AddIgnore(context.Context, string, string, string) error {
	s.t.Error("poll called AddIgnore")
	return nil
}

func (s pollStorage) SetForceCheck(context.Context, string, bool) (bool, error) {
	s.t.Error("poll called SetForceCheck")
	return false, nil
}

func (s pollStorage) PurgeDuplicateNames(context.Context, map[string][]string) error {
	s.t.Error("poll called PurgeDuplicateNames")
	return nil
}

type pollManager struct {
	t *testing.T
	// false: клиент как Transmission, Narrow не читает
	narrow bool
	// true: как qBittorrent, Narrow видит торренты без адреса, адрес есть только у оставленных
	brief   bool
	list    []*model.Entry
	err     error
	partial bool
	kept    [][]string
}

func (m *pollManager) GetEntries(_ context.Context, opts model.FilterOptions) ([]*model.Entry, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := m.list
	if m.narrow || m.brief {
		if opts.Narrow == nil {
			m.t.Fatal("poll passed no Narrow")
		}
		in := m.list
		if m.brief {
			in = make([]*model.Entry, 0, len(m.list))
			for _, e := range m.list {
				c := *e
				c.URL = ""
				in = append(in, &c)
			}
		}
		out = opts.Narrow(in)
		if m.brief {
			keep := make(map[string]struct{}, len(out))
			for _, e := range out {
				keep[e.Hash] = struct{}{}
			}
			out = nil
			for _, e := range m.list {
				if _, ok := keep[e.Hash]; ok {
					out = append(out, e)
				}
			}
		}
		names := make([]string, 0, len(out))
		for _, e := range out {
			names = append(names, e.Name)
		}
		slices.Sort(names)
		m.kept = append(m.kept, names)
	}
	if m.partial {
		return out, fmt.Errorf("%w: test", model.ErrPartialList)
	}
	return out, nil
}

func (m *pollManager) Submit(context.Context, *bytes.Buffer, model.ManagerAddFileOptions) error {
	m.t.Error("poll called Submit")
	return nil
}

func (m *pollManager) SubmitMagnet(context.Context, string, model.ManagerAddFileOptions) error {
	m.t.Error("poll called SubmitMagnet")
	return nil
}

func (m *pollManager) AttachComment(context.Context, string, string, func(context.Context) ([]byte, error)) error {
	m.t.Error("poll called AttachComment")
	return nil
}

type pollTracker struct {
	fakeTracker
	t   *testing.T
	ids map[string]int
}

func (p *pollTracker) GetID(link string) int {
	if id, ok := p.ids[link]; ok {
		return id
	}
	return 1
}

func (p *pollTracker) Adopt(context.Context, string, string) (string, error) {
	p.t.Error("poll called Adopt")
	return "", nil
}

func pollConfig() model.HandlerConfig {
	return model.HandlerConfig{
		Categories:        []string{"tv"},
		StartHour:         6,
		ListRefreshPerDay: 4,
		Location:          time.UTC,
		ThresholdMonth:    3,
		ThresholdHalfYear: 7,
		ThresholdYear:     14,
		ThresholdOld:      30,
	}
}

func newPollHandler(t *testing.T, store *fakeStorage, mgr *pollManager, cfg model.HandlerConfig) (*handler, *pollTracker) {
	mgr.t = t
	tr := &pollTracker{fakeTracker: fakeTracker{id: 1}, t: t, ids: map[string]int{}}
	return &handler{config: cfg, manager: mgr, storage: pollStorage{fakeStorage: store, t: t}, trackerService: tr}, tr
}

func pollEntry(hash, name, category, url string, added time.Time) *model.Entry {
	return &model.Entry{Hash: hash, Name: name, Category: category, Tag: "watch", URL: url, Timestamp: &added}
}

func addedNames(store *fakeStorage) []string {
	var names []string
	for _, a := range store.newAdds {
		names = append(names, a.entry.ItemName)
	}
	return names
}

func TestPollNewListsNewRelease(t *testing.T) {
	const url = "https://example.com/t/1"
	added := time.Now().Add(-time.Hour)
	store := &fakeStorage{}
	h, _ := newPollHandler(t, store, &pollManager{list: []*model.Entry{pollEntry("h1", "Show S01", "tv", url, added)}}, pollConfig())
	n, err := h.pollNew(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("pollNew = %d, %v, want 1, nil", n, err)
	}
	if len(store.newAdds) != 1 {
		t.Fatalf("inserted %d rows, want 1", len(store.newAdds))
	}
	got := store.newAdds[0]
	want := storageModel.CheckLogEntry{URLHash: storagePkg.URLHash(url), ItemName: "Show S01", ItemURL: url, TrackerTypeID: 1, Category: "tv"}
	if got.entry != want {
		t.Fatalf("row %+v, want %+v", got.entry, want)
	}
	if at := h.slotAtOrAfter(added.AddDate(0, 0, 3)); !got.at.Equal(at) {
		t.Fatalf("first check %v, want %v", got.at, at)
	}
}

func TestPollNewSkips(t *testing.T) {
	const url = "https://example.com/t/1"
	const other = "https://example.com/t/2"
	added := time.Now().Add(-time.Hour)
	cases := []struct {
		name  string
		list  []*model.Entry
		setup func(cfg *model.HandlerConfig, store *fakeStorage, tr *pollTracker)
	}{
		{name: "other category", list: []*model.Entry{pollEntry("h1", "Show S01", "movies", url, added)}},
		{
			name: "tags mode without the tag",
			list: []*model.Entry{{Hash: "h1", Name: "Show S01", Category: "tv", Tag: "other", URL: url, Timestamp: &added}},
			setup: func(cfg *model.HandlerConfig, _ *fakeStorage, _ *pollTracker) {
				cfg.Categories, cfg.WatchTags = nil, []string{"watch"}
			},
		},
		{name: "empty url", list: []*model.Entry{pollEntry("h1", "Show S01", "tv", "", added)}},
		{
			name:  "tracker unknown",
			list:  []*model.Entry{pollEntry("h1", "Show S01", "tv", url, added)},
			setup: func(_ *model.HandlerConfig, _ *fakeStorage, tr *pollTracker) { tr.ids[url] = 0 },
		},
		{
			name: "ignored",
			list: []*model.Entry{pollEntry("h1", "Show S01", "tv", url, added)},
			setup: func(_ *model.HandlerConfig, store *fakeStorage, _ *pollTracker) {
				store.ignore = map[string]struct{}{storagePkg.URLHash(url): {}}
			},
		},
		{
			name: "known hash",
			list: []*model.Entry{pollEntry("h1", "Show S01", "tv", url, added)},
			setup: func(_ *model.HandlerConfig, store *fakeStorage, _ *pollTracker) {
				store.knownHashes = map[string]struct{}{storagePkg.URLHash(url): {}}
			},
		},
		{
			name: "known name",
			list: []*model.Entry{pollEntry("h1", "Show S01", "tv", url, added)},
			setup: func(_ *model.HandlerConfig, store *fakeStorage, _ *pollTracker) {
				store.knownNames = []string{"  show s01 "}
			},
		},
		{
			name: "older same name unwatched",
			list: []*model.Entry{
				pollEntry("h1", "Show S01", "tv", url, added),
				pollEntry("h2", "Show S01", "movies", other, added.Add(-time.Hour)),
			},
		},
	}
	for _, c := range cases {
		for _, narrow := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s narrow=%v", c.name, narrow), func(t *testing.T) {
				// контрольный релиз рядом: без него заглушка, которая ничего не пишет, прошла бы тест
				list := append(slices.Clone(c.list), pollEntry("hc", "Control", "tv", "https://example.com/t/9", added))
				store := &fakeStorage{}
				cfg := pollConfig()
				h, tr := newPollHandler(t, store, &pollManager{narrow: narrow, list: list}, cfg)
				if c.setup != nil {
					c.setup(&h.config, store, tr)
				}
				n, err := h.pollNew(context.Background())
				if err != nil || n != 1 {
					t.Fatalf("pollNew = %d, %v, want 1, nil", n, err)
				}
				if got := addedNames(store); !slices.Equal(got, []string{"Control"}) {
					t.Fatalf("inserted %v, want [Control]", got)
				}
			})
		}
	}
}

func TestPollNewPartialListInsertsNothing(t *testing.T) {
	store := &fakeStorage{}
	h, _ := newPollHandler(t, store, &pollManager{partial: true, list: []*model.Entry{
		pollEntry("h1", "Show S01", "tv", "https://example.com/t/1", time.Now()),
	}}, pollConfig())
	n, err := h.pollNew(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("pollNew = %d, %v, want 0, nil", n, err)
	}
	if len(store.newAdds) != 0 {
		t.Fatalf("inserted %v on a partial list", addedNames(store))
	}
}

func TestPollNewManagerError(t *testing.T) {
	store := &fakeStorage{}
	h, _ := newPollHandler(t, store, &pollManager{err: errors.New("qbit down")}, pollConfig())
	if n, err := h.pollNew(context.Background()); err == nil || n != 0 {
		t.Fatalf("pollNew = %d, %v, want 0 and an error", n, err)
	}
	if len(store.newAdds) != 0 {
		t.Fatalf("inserted %v on a manager error", addedNames(store))
	}
}

func TestPollNewOldTorrentDueNow(t *testing.T) {
	store := &fakeStorage{}
	h, _ := newPollHandler(t, store, &pollManager{list: []*model.Entry{
		pollEntry("h1", "Old Show", "tv", "https://example.com/t/1", time.Now().AddDate(0, 0, -400)),
	}}, pollConfig())
	before := time.Now()
	if _, err := h.pollNew(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	if len(store.newAdds) != 1 {
		t.Fatalf("inserted %d rows, want 1", len(store.newAdds))
	}
	if at := store.newAdds[0].at; at.Before(before) || at.After(after) {
		t.Fatalf("first check %v, want within [%v, %v]", at, before, after)
	}
}

func TestPollNewNarrowsAndCaches(t *testing.T) {
	added := time.Now().Add(-time.Hour)
	k := pollEntry("hk", "Known", "tv", "https://example.com/t/1", added)
	n := pollEntry("hn", "New", "tv", "https://example.com/t/2", added)
	u := pollEntry("hu", "Unk", "tv", "https://example.com/t/3", added)
	w := pollEntry("hw", "Wat", "movies", "https://example.com/t/4", added)
	store := &fakeStorage{knownNames: []string{"known"}}
	mgr := &pollManager{narrow: true, list: []*model.Entry{k, n, u, w}}
	h, tr := newPollHandler(t, store, mgr, pollConfig())
	tr.ids[u.URL] = 0
	poll := func(list ...*model.Entry) {
		t.Helper()
		mgr.list = list
		if _, err := h.pollNew(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	poll(k, n, u, w)
	poll(k, n, u, w)
	poll(k, n, w)
	poll(k, n, u, w)
	want := [][]string{{"New", "Unk"}, {}, {}, {"Unk"}}
	if !slices.EqualFunc(mgr.kept, want, slices.Equal[[]string]) {
		t.Fatalf("kept %v, want %v", mgr.kept, want)
	}
	if got := addedNames(store); !slices.Equal(got, []string{"New"}) {
		t.Fatalf("inserted %v, want [New]", got)
	}
}

func TestPollNewRunsDuringPass(t *testing.T) {
	store := &fakeStorage{}
	h, _ := newPollHandler(t, store, &pollManager{list: []*model.Entry{
		pollEntry("h1", "Show S01", "tv", "https://example.com/t/1", time.Now()),
	}}, pollConfig())
	h.state = NewState()
	h.gen = h.state.Reset()
	h.state.Begin(h.gen)
	defer h.state.End(h.gen)
	if n, err := h.pollNew(context.Background()); err != nil || n != 1 {
		t.Fatalf("pollNew during a pass = %d, %v, want 1, nil", n, err)
	}
	if !h.state.Running() {
		t.Fatal("poll ended the pass")
	}
}

func TestRunRegistersPoller(t *testing.T) {
	store := &fakeStorage{}
	h, _ := newPollHandler(t, store, &pollManager{list: []*model.Entry{
		pollEntry("h1", "Show S01", "tv", "https://example.com/t/1", time.Now()),
	}}, pollConfig())
	h.state = NewState()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		h.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(time.Second)
	for {
		n, err := h.state.PollNew(context.Background())
		if err == nil {
			if n != 1 {
				t.Fatalf("PollNew = %d, want 1", n)
			}
			break
		}
		if !errors.Is(err, ErrPollUnavailable) || time.Now().After(deadline) {
			t.Fatalf("PollNew error %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
	if _, err := h.state.PollNew(context.Background()); !errors.Is(err, ErrPollUnavailable) {
		t.Fatalf("PollNew after Run = %v, want ErrPollUnavailable", err)
	}
}

func TestPollNewRetriesEmptyURLAfterTTL(t *testing.T) {
	ttl := pollEmptyTTL
	defer func() { pollEmptyTTL = ttl }()
	pollEmptyTTL = time.Hour
	e := pollEntry("he", "Magnet", "tv", "", time.Now().Add(-time.Hour))
	mgr := &pollManager{narrow: true, list: []*model.Entry{e}}
	h, _ := newPollHandler(t, &fakeStorage{}, mgr, pollConfig())
	for range 2 {
		if _, err := h.pollNew(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	pollEmptyTTL = 0
	if _, err := h.pollNew(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"Magnet"}, {}, {"Magnet"}}
	if !slices.EqualFunc(mgr.kept, want, slices.Equal[[]string]) {
		t.Fatalf("kept %v, want %v", mgr.kept, want)
	}
}

func withPollState(h *handler) {
	h.state = NewState()
	h.gen = h.state.Reset()
	h.state.SetPoller(h.gen, func(context.Context) (int, error) { return 0, nil })
}

func TestPollNewMarksAbsent(t *testing.T) {
	const u2 = "https://example.com/t/2"
	const u3 = "https://example.com/t/3"
	added := time.Now().Add(-time.Hour)
	p2, p3 := storagePkg.URLHash(u2), storagePkg.URLHash(u3)
	store := &fakeStorage{
		listedNames: map[string]string{"p1": "Show A", p2: "Old Name", p3: "Third", "x": "Gone", "n": "Gone new"},
		listedFresh: map[string]string{p3: "tv", "n": ""},
		knownHashes: map[string]struct{}{p3: {}},
		knownNames:  []string{"Show A", "Old Name", "Third", "Gone", "Gone new"},
	}
	list := []*model.Entry{
		pollEntry("ih1", "  show a", "movies", "", added),
		pollEntry("ih2", "New Name", "movies", u2, added),
		pollEntry("ih3", "Renamed Third", "tv", u3, added),
	}
	mgr := &pollManager{brief: true, list: list}
	h, _ := newPollHandler(t, store, mgr, pollConfig())
	withPollState(h)
	h.siblings.Store(&map[string][]string{p2: {"ih2"}})

	n, err := h.pollNew(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("poll 1 = %d, %v, want 2, nil", n, err)
	}
	if !slices.Equal(store.droppedNew, []string{"n"}) {
		t.Fatalf("poll 1 dropped %v, want [n]", store.droppedNew)
	}
	if got := h.state.Absent(); len(got) != 1 || !hasKey(got, "x") {
		t.Fatalf("poll 1 absent %v, want {x}", got)
	}
	if len(store.newAdds) != 0 {
		t.Fatalf("inserted %v", addedNames(store))
	}

	delete(store.listedNames, "n")
	delete(store.listedFresh, "n")
	mgr.list = append(slices.Clone(list), pollEntry("ihx", "Gone", "movies", "", added))
	n, err = h.pollNew(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("poll 2 = %d, %v, want 1, nil", n, err)
	}
	if !hasKey(h.pollSkip, "ih3") {
		t.Fatal("ih3 is not in pollSkip")
	}
	if !slices.Equal(store.droppedNew, []string{"n"}) {
		t.Fatalf("poll 2 dropped %v, want [n]", store.droppedNew)
	}
	if got := h.state.Absent(); len(got) != 0 {
		t.Fatalf("poll 2 absent %v, want empty", got)
	}
}

func hasKey(m map[string]struct{}, k string) bool {
	_, ok := m[k]
	return ok
}

func TestPollNewAbsentGuards(t *testing.T) {
	added := time.Now().Add(-time.Hour)
	other := pollEntry("ih1", "Other", "movies", "", added)
	cases := []struct {
		name      string
		mgr       pollManager
		listedErr error
		wantErr   bool
	}{
		{name: "partial list", mgr: pollManager{brief: true, partial: true, list: []*model.Entry{other}}},
		{name: "manager error", mgr: pollManager{err: errors.New("qbit down")}, wantErr: true},
		{name: "no entries", mgr: pollManager{brief: true}},
		{name: "rows read error", mgr: pollManager{brief: true, list: []*model.Entry{other}}, listedErr: errors.New("db"), wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &fakeStorage{
				listedNames: map[string]string{"x": "Gone"},
				listedFresh: map[string]string{"x": ""},
				listedErr:   c.listedErr,
			}
			mgr := c.mgr
			h, _ := newPollHandler(t, store, &mgr, pollConfig())
			withPollState(h)
			h.state.SetAbsent(h.gen, map[string]struct{}{"y": {}})
			n, err := h.pollNew(context.Background())
			if n != 0 || (err != nil) != c.wantErr {
				t.Fatalf("pollNew = %d, %v, want 0, error %v", n, err, c.wantErr)
			}
			if len(store.droppedNew) != 0 {
				t.Fatalf("dropped %v", store.droppedNew)
			}
			if got := h.state.Absent(); len(got) != 1 || !hasKey(got, "y") {
				t.Fatalf("absent %v, want {y}", got)
			}
		})
	}
}

type pollOrderStorage struct {
	pollStorage
	log *[]string
}

func (s pollOrderStorage) ListedReleases(ctx context.Context) (map[string]string, map[string]string, error) {
	*s.log = append(*s.log, "listed")
	return s.pollStorage.ListedReleases(ctx)
}

type pollOrderManager struct {
	*pollManager
	log *[]string
}

func (m pollOrderManager) GetEntries(ctx context.Context, opts model.FilterOptions) ([]*model.Entry, error) {
	*m.log = append(*m.log, "list")
	return m.pollManager.GetEntries(ctx, opts)
}

func TestPollNewReadsRowsBeforeList(t *testing.T) {
	var log []string
	store := &fakeStorage{}
	mgr := &pollManager{brief: true, list: []*model.Entry{pollEntry("ih1", "Other", "movies", "", time.Now())}}
	h, _ := newPollHandler(t, store, mgr, pollConfig())
	h.storage = pollOrderStorage{pollStorage: pollStorage{fakeStorage: store, t: t}, log: &log}
	h.manager = pollOrderManager{pollManager: mgr, log: &log}
	if _, err := h.pollNew(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(log, []string{"listed", "list"}) {
		t.Fatalf("calls %v, want [listed list]", log)
	}
}

func TestPollNewUpdatesListedCategory(t *testing.T) {
	const url = "https://example.com/t/1"
	k := storagePkg.URLHash(url)
	store := &fakeStorage{
		knownHashes: map[string]struct{}{k: {}},
		knownNames:  []string{"Show S01"},
		listedNames: map[string]string{k: "Show S01"},
		listedFresh: map[string]string{k: ""},
	}
	list := []*model.Entry{pollEntry("h1", "Show S01", "tv", url, time.Now().Add(-time.Hour))}
	h, _ := newPollHandler(t, store, &pollManager{brief: true, list: list}, pollConfig())
	n, err := h.pollNew(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("pollNew = %d, %v, want 1, nil", n, err)
	}
	if !maps.Equal(store.newCats, map[string]string{k: "tv"}) {
		t.Fatalf("newCats = %v, want {%s: tv}", store.newCats, k)
	}
}

func TestPollNewCategoryGuards(t *testing.T) {
	const url = "https://example.com/t/1"
	k := storagePkg.URLHash(url)
	added := time.Now().Add(-time.Hour)
	cases := []struct {
		name      string
		mgr       pollManager
		fresh     map[string]string
		names     map[string]string
		newCatErr error
		wantN     int
		wantCalls int
		wantCats  map[string]string
	}{
		{
			name:  "same category",
			mgr:   pollManager{brief: true, list: []*model.Entry{pollEntry("h1", "Show S01", "tv", url, added)}},
			fresh: map[string]string{k: "tv"},
			names: map[string]string{k: "Show S01"},
		},
		{
			name:      "renamed torrent updates by url",
			mgr:       pollManager{list: []*model.Entry{pollEntry("h1", "Renamed", "tv", url, added)}},
			fresh:     map[string]string{k: "movies"},
			names:     map[string]string{k: "Old name"},
			wantN:     1,
			wantCalls: 1,
			wantCats:  map[string]string{k: "tv"},
		},
		{
			name:  "partial list",
			mgr:   pollManager{brief: true, partial: true, list: []*model.Entry{pollEntry("h1", "Show S01", "tv", url, added)}},
			fresh: map[string]string{k: ""},
			names: map[string]string{k: "Show S01"},
		},
		{
			name:  "empty list",
			mgr:   pollManager{brief: true},
			fresh: map[string]string{k: ""},
			names: map[string]string{k: "Show S01"},
		},
		{
			name:      "update error does not fail the poll",
			mgr:       pollManager{brief: true, list: []*model.Entry{pollEntry("h1", "Show S01", "tv", url, added)}},
			fresh:     map[string]string{k: ""},
			names:     map[string]string{k: "Show S01"},
			newCatErr: errors.New("db"),
			wantCalls: 1,
		},
		{
			name:  "row not fresh",
			mgr:   pollManager{brief: true, list: []*model.Entry{pollEntry("h1", "Show S01", "tv", url, added)}},
			names: map[string]string{k: "Show S01"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &fakeStorage{
				knownHashes: map[string]struct{}{k: {}},
				knownNames:  []string{"Show S01"},
				listedNames: c.names,
				listedFresh: c.fresh,
				newCatErr:   c.newCatErr,
			}
			mgr := c.mgr
			h, _ := newPollHandler(t, store, &mgr, pollConfig())
			n, err := h.pollNew(context.Background())
			if err != nil || n != c.wantN {
				t.Fatalf("pollNew = %d, %v, want %d, nil", n, err, c.wantN)
			}
			if store.newCatCalls != c.wantCalls {
				t.Fatalf("UpdateNewCategories calls = %d, want %d", store.newCatCalls, c.wantCalls)
			}
			if !maps.Equal(store.newCats, c.wantCats) {
				t.Fatalf("newCats = %v, want %v", store.newCats, c.wantCats)
			}
		})
	}
}

func TestPollNewKeepsCategoryWithoutMatch(t *testing.T) {
	const url = "https://example.com/t/1"
	k := storagePkg.URLHash(url)
	store := &fakeStorage{
		knownHashes: map[string]struct{}{k: {}},
		knownNames:  []string{"Foo"},
		listedNames: map[string]string{k: "Foo"},
		listedFresh: map[string]string{k: "tv"},
	}
	list := []*model.Entry{pollEntry("h1", "foo", "movies", "", time.Now().Add(-time.Hour))}
	h, _ := newPollHandler(t, store, &pollManager{brief: true, list: list}, pollConfig())
	n, err := h.pollNew(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("pollNew = %d, %v, want 0, nil", n, err)
	}
	if store.newCatCalls != 0 || len(store.newCats) != 0 {
		t.Fatalf("calls = %d, newCats = %v, want no update", store.newCatCalls, store.newCats)
	}
	if len(store.droppedNew) != 0 {
		t.Fatalf("dropped %v, want none", store.droppedNew)
	}
}
