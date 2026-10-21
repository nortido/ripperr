package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/model"
	"ripperr/internal/model/storage"
	"ripperr/internal/runner"
	storagePkg "ripperr/internal/storage"
)

type commentRecordingClient struct {
	entries []*model.Entry
	calls   int
	got     model.RedownloadOptions
	err     error
	listErr error
	onForce func()
}

func (c *commentRecordingClient) GetCategories(context.Context) ([]string, error) { return nil, nil }

func (c *commentRecordingClient) List(context.Context, model.FilterOptions) ([]*model.Entry, error) {
	return c.entries, c.listErr
}

func (c *commentRecordingClient) ForceRedownload(_ context.Context, _ string, opts model.RedownloadOptions) error {
	c.calls++
	c.got = opts
	if c.onForce != nil {
		c.onForce()
	}
	return c.err
}

func TestHandleRedownloadRewritesCommentToPrimaryDomain(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()

	effective := func() config.App {
		cfg := testEffective()
		cfg.Tracker = config.Tracker{
			"rutracker": {Name: "RuTracker", Domain: "rutracker.org", Mirrors: []string{"rutracker.net"}, ActiveDomain: "rutracker.nl", Enabled: true},
		}
		return cfg
	}
	srv := New(&fakeStore{}, func() {}, effective, ":0", runner.NewState(), nil)

	cases := []struct {
		name    string
		comment string
		want    string
	}{
		{"mirror", "https://rutracker.net/forum/viewtopic.php?t=1", "https://rutracker.org/forum/viewtopic.php?t=1"},
		{"active domain keeps scheme and query", "http://rutracker.nl/forum/viewtopic.php?t=2&start=30", "http://rutracker.org/forum/viewtopic.php?t=2&start=30"},
		{"primary already", "https://rutracker.org/forum/viewtopic.php?t=3", ""},
		{"unknown host", "https://example.com/topic/4", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := &commentRecordingClient{entries: []*model.Entry{{URL: tc.comment, Hash: "abc", Name: "x"}}}
			managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) { return fc, nil }

			urlHash := storagePkg.URLHash(tc.comment)
			req := httptest.NewRequest(http.MethodPost, "/api/history/"+urlHash+"/redownload", nil)
			req.SetPathValue("hash", urlHash)
			w := httptest.NewRecorder()
			srv.handleRedownload(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
			}
			if fc.calls != 1 {
				t.Fatalf("ForceRedownload calls = %d, want 1", fc.calls)
			}
			if fc.got.Comment != tc.want {
				t.Errorf("comment = %q, want %q", fc.got.Comment, tc.want)
			}
		})
	}
}

type ignoreRecordingStore struct {
	*fakeStore
	ops []string
}

func (s *ignoreRecordingStore) AddIgnore(ctx context.Context, hash, name, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.ops = append(s.ops, "add "+hash)
	return s.fakeStore.AddIgnore(ctx, hash, name, url)
}

func (s *ignoreRecordingStore) DeleteIgnore(ctx context.Context, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.ops = append(s.ops, "del "+hash)
	kept := s.ignore[:0]
	for _, e := range s.ignore {
		if e.URLHash != hash {
			kept = append(kept, e)
		}
	}
	s.ignore = kept
	return nil
}

func TestHandleRedownloadKeepsIgnoreMark(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()

	effective := func() config.App {
		cfg := testEffective()
		cfg.Tracker = config.Tracker{
			"rutracker": {Name: "RuTracker", Domain: "rutracker.org", Mirrors: []string{"rutracker.net"}, Enabled: true},
		}
		return cfg
	}
	const mirror = "https://rutracker.net/forum/viewtopic.php?t=1"
	const primary = "https://rutracker.org/forum/viewtopic.php?t=1"

	cases := []struct {
		name     string
		comment  string
		ignored  bool
		forceErr error
		wantCode int
		wantOps  []string
		sibling  bool
		partial  bool
		cancel   bool
	}{
		{"ignored mirror moves mark", mirror, true, nil, http.StatusOK,
			[]string{"add " + storagePkg.URLHash(primary), "del " + storagePkg.URLHash(mirror)}, false, false, false},
		{"not ignored", mirror, false, nil, http.StatusOK, nil, false, false, false},
		{"primary already", primary, true, nil, http.StatusOK, nil, false, false, false},
		{"redownload fails", mirror, true, errors.New("boom"), http.StatusBadGateway, nil, false, false, false},
		{"sibling keeps old mark", mirror, true, nil, http.StatusOK,
			[]string{"add " + storagePkg.URLHash(primary)}, true, false, false},
		{"partial list keeps old mark", mirror, true, nil, http.StatusOK,
			[]string{"add " + storagePkg.URLHash(primary)}, false, true, false},
		{"client gone after redownload", mirror, true, nil, http.StatusOK,
			[]string{"add " + storagePkg.URLHash(primary), "del " + storagePkg.URLHash(mirror)}, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			urlHash := storagePkg.URLHash(tc.comment)
			st := &ignoreRecordingStore{fakeStore: &fakeStore{}}
			if tc.ignored {
				st.ignore = []storage.IgnoreEntry{{URLHash: urlHash, ItemName: "Release", ItemURL: tc.comment}}
			}
			srv := New(st, func() {}, effective, ":0", runner.NewState(), nil)
			ents := []*model.Entry{{URL: tc.comment, Hash: "abc", Name: "x"}}
			if tc.sibling {
				ents = append(ents, &model.Entry{URL: tc.comment, Hash: "def", Name: "x"})
			}
			fc := &commentRecordingClient{entries: ents, err: tc.forceErr}
			if tc.partial {
				fc.listErr = model.ErrPartialList
			}
			managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) { return fc, nil }

			req := httptest.NewRequest(http.MethodPost, "/api/history/"+urlHash+"/redownload", nil)
			if tc.cancel {
				reqCtx, cancel := context.WithCancel(req.Context())
				defer cancel()
				req = req.WithContext(reqCtx)
				fc.onForce = cancel
			}
			req.SetPathValue("hash", urlHash)
			w := httptest.NewRecorder()
			srv.handleRedownload(w, req)
			if w.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d, body: %s", w.Code, tc.wantCode, w.Body.String())
			}
			if strings.Join(st.ops, "; ") != strings.Join(tc.wantOps, "; ") {
				t.Fatalf("ignore ops = %q, want %q", st.ops, tc.wantOps)
			}
			if tc.wantOps == nil {
				return
			}
			set, _ := st.GetIgnoreSet(context.Background())
			if _, ok := set[storagePkg.URLHash(primary)]; !ok {
				t.Errorf("ignore set %v misses the new comment hash", set)
			}
			if _, ok := set[urlHash]; ok != (tc.sibling || tc.partial) {
				t.Errorf("old mark kept = %v, want %v", ok, tc.sibling || tc.partial)
			}
			if got := st.ignore[len(st.ignore)-1]; got.ItemName != "Release" || got.ItemURL != primary {
				t.Errorf("moved entry = %+v, want name Release and url %s", got, primary)
			}
		})
	}
}
