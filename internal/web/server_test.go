package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/helper/breakeralerts"
	"ripperr/internal/manager/client/deluge"
	"ripperr/internal/manager/client/downloadstation"
	"ripperr/internal/manager/client/transmission"
	"ripperr/internal/model"
	"ripperr/internal/model/storage"
	"ripperr/internal/model/tracker"
	"ripperr/internal/notify"
	"ripperr/internal/runner"
	"ripperr/internal/settings"
	storagePkg "ripperr/internal/storage"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker/registry"
	_ "ripperr/internal/tracker/registry/all"
	"ripperr/internal/webpush"
)

type fakeStore struct {
	checkLog         []storage.CheckLogEntry
	ignore           []storage.IgnoreEntry
	settings         map[string]json.RawMessage
	forceItems       map[string]bool
	pingErr          error
	summary          storage.CheckLogSummary
	resetStreakCalls []string
	forceAllFilter   storage.CheckLogFilter
	purgedCategories []string
	purgedRows       int64
	deletedTrackers  []int
	newReleases      map[string]bool
}

func dedupeCheckLogByName(items []storage.CheckLogEntry) []storage.CheckLogEntry {
	type pick struct {
		entry storage.CheckLogEntry
	}
	best := make(map[string]pick)
	for _, e := range items {
		key := strings.ToLower(strings.TrimSpace(e.ItemName))
		cur, ok := best[key]
		if !ok || e.CheckedAt.After(cur.entry.CheckedAt) || (e.CheckedAt.Equal(cur.entry.CheckedAt) && e.ID > cur.entry.ID) {
			best[key] = pick{entry: e}
		}
	}
	out := make([]storage.CheckLogEntry, 0, len(best))
	seen := make(map[string]struct{})
	for _, e := range items {
		key := strings.ToLower(strings.TrimSpace(e.ItemName))
		if _, ok := seen[key]; ok {
			continue
		}
		w := best[key].entry
		if w.URLHash == e.URLHash && w.ID == e.ID {
			out = append(out, e)
			seen[key] = struct{}{}
		}
	}
	return out
}

func (f *fakeStore) ListCheckLog(_ context.Context, filter storage.CheckLogFilter) ([]storage.CheckLogEntry, int, error) {
	ignoreSet := make(map[string]struct{}, len(f.ignore))
	for _, e := range f.ignore {
		ignoreSet[e.URLHash] = struct{}{}
	}
	var filtered []storage.CheckLogEntry
	nameNeedle := strings.ToLower(filter.Name)
	for _, e := range dedupeCheckLogByName(f.checkLog) {
		if filter.Status == "broken" {
			brokenStreak := filter.BrokenStreak
			if brokenStreak < 1 {
				brokenStreak = 1
			}
			if e.ErrorStreak < brokenStreak {
				continue
			}
		} else if filter.Status != "" && e.Status != filter.Status {
			continue
		}
		if filter.Name != "" && !strings.Contains(strings.ToLower(e.ItemName), nameNeedle) {
			continue
		}
		_, inIgnore := ignoreSet[e.URLHash]
		if filter.IgnoredOnly && !inIgnore {
			continue
		}
		filtered = append(filtered, e)
	}
	if filter.SortBy == "recheck" {
		sort.Slice(filtered, func(i, j int) bool {
			a, b := filtered[i].RecheckAt, filtered[j].RecheckAt
			if a == nil && b == nil {
				return false
			}
			if a == nil {
				return false
			}
			if b == nil {
				return true
			}
			return a.Before(*b)
		})
	}
	total := len(filtered)
	if filter.Offset > len(filtered) {
		return nil, total, nil
	}
	filtered = filtered[filter.Offset:]
	if filter.Limit > 0 && len(filtered) > filter.Limit {
		filtered = filtered[:filter.Limit]
	}
	return filtered, total, nil
}

func (f *fakeStore) ListIgnore(context.Context) ([]storage.IgnoreEntry, error) {
	return f.ignore, nil
}

func (f *fakeStore) GetIgnoreSet(_ context.Context) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(f.ignore))
	for _, e := range f.ignore {
		set[e.URLHash] = struct{}{}
	}
	return set, nil
}

func (f *fakeStore) AddIgnore(_ context.Context, hash, name, url string) error {
	f.ignore = append(f.ignore, storage.IgnoreEntry{URLHash: hash, ItemName: name, ItemURL: url, AddedAt: time.Now()})
	return nil
}

func (f *fakeStore) DeleteIgnore(_ context.Context, hash string) error {
	return nil
}

func (f *fakeStore) ResetErrorStreak(_ context.Context, hash string) error {
	f.resetStreakCalls = append(f.resetStreakCalls, hash)
	return nil
}

func (f *fakeStore) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	v, ok := f.settings[key]
	return v, ok, nil
}

func (f *fakeStore) SaveSetting(_ context.Context, key string, value json.RawMessage) error {
	if f.settings == nil {
		f.settings = make(map[string]json.RawMessage)
	}
	f.settings[key] = value
	return nil
}

func (f *fakeStore) SetForceCheck(_ context.Context, urlHash string, force bool) (bool, error) {
	if f.forceItems == nil {
		return false, nil
	}
	if _, ok := f.forceItems[urlHash]; !ok {
		return false, nil
	}
	f.forceItems[urlHash] = force
	return true, nil
}

func (f *fakeStore) IsNewRelease(_ context.Context, urlHash string) (bool, error) {
	return f.newReleases[urlHash], nil
}

func (f *fakeStore) ForceCheckAll(_ context.Context, force bool, filter storage.CheckLogFilter) ([]string, int64, error) {
	f.forceAllFilter = filter
	if f.forceItems == nil {
		return nil, 0, nil
	}
	ignoreSet, _ := f.GetIgnoreSet(context.Background())
	var hashes []string
	var n int64
	for hash := range f.forceItems {
		if force {
			if _, ignored := ignoreSet[hash]; ignored {
				continue
			}
			hashes = append(hashes, hash)
			if !f.forceItems[hash] {
				f.forceItems[hash] = true
				n++
			}
		} else {
			hashes = append(hashes, hash)
			if f.forceItems[hash] {
				f.forceItems[hash] = false
				n++
			}
		}
	}
	return hashes, n, nil
}

func (f *fakeStore) CheckLogSummary(context.Context) (storage.CheckLogSummary, error) {
	return f.summary, nil
}

func (f *fakeStore) GetTracker(context.Context, int) (*tracker.Tracker, error) {
	return &tracker.Tracker{}, nil
}

func (f *fakeStore) PurgeCategoriesExcept(_ context.Context, allowed []string) (int64, error) {
	f.purgedCategories = allowed
	return f.purgedRows, nil
}

func (f *fakeStore) DeleteTracker(_ context.Context, trackerID int) error {
	f.deletedTrackers = append(f.deletedTrackers, trackerID)
	return nil
}

func (f *fakeStore) Ping(context.Context) error {
	return f.pingErr
}

func newBareServer(store *fakeStore) *Server {
	return New(store, func() {}, testEffective, ":0", runner.NewState(), nil)
}

var testSessionSecret = []byte("0123456789abcdef0123456789abcdef")

func withTestSession(srv *Server) *Server {
	// с аккаунтом в кеше API уже не открыт, поэтому куку подставляем, если у теста нет своей
	srv.authCache.set(&authDoc{Username: "admin", Secret: base64.RawURLEncoding.EncodeToString(testSessionSecret)})
	next := srv.srv.Handler
	srv.srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie(authCookieName); err != nil {
			r.AddCookie(&http.Cookie{Name: authCookieName, Value: signSession(testSessionSecret, "admin", time.Now().Add(authSessionTTL))})
		}
		// и заголовок от CSRF, если тест не задал свой
		if r.Header.Get("X-Ripperr") == "" {
			r.Header.Set("X-Ripperr", "1")
		}
		next.ServeHTTP(w, r)
	})
	return srv
}

func newTestServer(store *fakeStore) *Server {
	return withTestSession(newBareServer(store))
}

func testEffective() config.App {
	return config.App{
		Log:     config.Log{Level: "info"},
		Runner:  config.Runner{StartHour: 7, ListRefreshPerDay: 2, MonthThresholdDays: 1, HalfYearThresholdDays: 7, YearThresholdDays: 30, WatchTags: []string{"watch"}},
		Tracker: config.Tracker{},
		Manager: config.Manager{QbitTorrent: struct {
			Host     string `env:"QBIT_URL, default=localhost:8068"`
			Username string `env:"QBIT_USER, default=admin"`
			Password string `env:"QBIT_PASS, default=adminadmin"`
		}{}},
		Flaresolverr: config.Flaresolverr{MaxTimeout: 300000},
	}
}

type fakeQbitClient struct {
	cats               []string
	entries            []*model.Entry
	err                error
	forceRedownloadErr error
	redownloadDelay    time.Duration
	redownloadTracker  *concurrencyTracker
}

func (f *fakeQbitClient) GetCategories(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.cats, nil
}

func (f *fakeQbitClient) List(context.Context, model.FilterOptions) ([]*model.Entry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.entries, nil
}

func (f *fakeQbitClient) ForceRedownload(ctx context.Context, _ string, _ model.RedownloadOptions) error {
	if f.redownloadTracker != nil {
		f.redownloadTracker.enter()
		defer f.redownloadTracker.leave()
	}
	if f.redownloadDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(f.redownloadDelay):
		}
	}
	return f.forceRedownloadErr
}

type concurrencyTracker struct {
	mu      sync.Mutex
	current int
	max     int
}

func (c *concurrencyTracker) enter() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current++
	if c.current > c.max {
		c.max = c.current
	}
}

func (c *concurrencyTracker) leave() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current--
}

func (c *concurrencyTracker) peak() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max
}

func TestStaticCacheHeaders(t *testing.T) {
	srv := newTestServer(&fakeStore{})
	handler := srv.srv.Handler

	req := httptest.NewRequest(http.MethodGet, "/base.css", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-cache") {
		t.Fatalf("Cache-Control = %q, want no-cache", w.Header().Get("Cache-Control"))
	}
	etag := w.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag empty")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/base.css", nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", w2.Code)
	}
}

func TestGetCategories(t *testing.T) {
	fakeQbitFactory := func(cats []string, err error) func(string, model.ManagerConfig) (managerClient, error) {
		return func(_ string, _ model.ManagerConfig) (managerClient, error) {
			if err != nil {
				return nil, err
			}
			return &fakeQbitClient{cats: cats}, nil
		}
	}

	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()

	store := &fakeStore{}
	srv := newTestServer(store)

	managerClientFactory = fakeQbitFactory([]string{"tv", "movies"}, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/categories", nil)
	w := httptest.NewRecorder()
	srv.handleGetCategories(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Categories []string `json:"categories"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Categories) != 2 || resp.Categories[0] != "tv" || resp.Categories[1] != "movies" {
		t.Fatalf("categories = %v", resp.Categories)
	}

	managerClientFactory = fakeQbitFactory(nil, fmt.Errorf("connection refused"))
	req = httptest.NewRequest(http.MethodGet, "/api/categories", nil)
	w = httptest.NewRecorder()
	srv.handleGetCategories(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
}

func TestPutScheduleValidation(t *testing.T) {
	store := &fakeStore{}
	reloaded := false
	srv := New(store, func() { reloaded = true }, testEffective, ":0", runner.NewState(), nil)

	body := []byte(`{"start_hour":25,"list_refresh_per_day":2,"threshold_month":1,"threshold_half_year":1,"threshold_year":1}`)
	req := httptest.NewRequest(http.MethodPut, "/api/schedule", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutSchedule(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	body = []byte(`{"start_hour":8,"list_refresh_per_day":4,"threshold_month":1,"threshold_half_year":2,"threshold_year":3}`)
	req = httptest.NewRequest(http.MethodPut, "/api/schedule", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handlePutSchedule(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if store.settings["schedule"] == nil {
		t.Fatal("schedule not saved")
	}
	if !reloaded {
		t.Error("reload not called")
	}
}

func TestPutScheduleMagnetTimeout(t *testing.T) {
	store := &fakeStore{}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)

	body := []byte(`{"start_hour":8,"list_refresh_per_day":4,"threshold_month":1,"threshold_half_year":2,"threshold_year":3,"magnet_timeout":601}`)
	req := httptest.NewRequest(http.MethodPut, "/api/schedule", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutSchedule(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for out-of-range magnet_timeout", w.Code)
	}

	body = []byte(`{"start_hour":8,"list_refresh_per_day":4,"threshold_month":1,"threshold_half_year":2,"threshold_year":3,"magnet_timeout":90}`)
	req = httptest.NewRequest(http.MethodPut, "/api/schedule", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handlePutSchedule(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 for valid magnet_timeout", w.Code)
	}
}

func TestPutScheduleCategoryMerge(t *testing.T) {
	store := &fakeStore{}
	effective := func() config.App {
		cfg := testEffective()
		cfg.Runner.AllowedCategories = []string{"env-cat"}
		cfg.Manager.DeleteFilesCategories = []string{"env-del"}
		_ = settings.ApplyOverrides(context.Background(), &cfg, store)
		return cfg
	}
	srv := New(store, func() {}, effective, ":0", runner.NewState(), nil)

	base := `{"start_hour":8,"list_refresh_per_day":2,"threshold_month":1,"threshold_half_year":2,"threshold_year":3`
	withCats := base + `,"monitored_categories":["movies"],"delete_files_categories":["tv"]}`
	noCats := `{"start_hour":9,"list_refresh_per_day":4,"threshold_month":1,"threshold_half_year":2,"threshold_year":3}`
	clearCats := base + `,"monitored_categories":[],"delete_files_categories":[]}`

	put := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/schedule", bytes.NewReader([]byte(body)))
		w := httptest.NewRecorder()
		srv.handlePutSchedule(w, req)
		if w.Code != http.StatusNoContent {
			t.Fatalf("PUT status = %d, want 204, body: %s", w.Code, body)
		}
	}
	get := func() map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/schedule", nil)
		w := httptest.NewRecorder()
		srv.handleGetSchedule(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET status = %d, want 200", w.Code)
		}
		var sch map[string]any
		if err := json.NewDecoder(w.Body).Decode(&sch); err != nil {
			t.Fatal(err)
		}
		return sch
	}
	catsEqual := func(sch map[string]any, monitored, deleteFiles []string) {
		t.Helper()
		gotMon, _ := sch["monitored_categories"].([]any)
		gotDel, _ := sch["delete_files_categories"].([]any)
		if len(gotMon) != len(monitored) {
			t.Fatalf("monitored_categories = %v, want %v", gotMon, monitored)
		}
		for i, v := range monitored {
			if gotMon[i] != v {
				t.Fatalf("monitored_categories[%d] = %v, want %v", i, gotMon[i], v)
			}
		}
		if len(gotDel) != len(deleteFiles) {
			t.Fatalf("delete_files_categories = %v, want %v", gotDel, deleteFiles)
		}
		for i, v := range deleteFiles {
			if gotDel[i] != v {
				t.Fatalf("delete_files_categories[%d] = %v, want %v", i, gotDel[i], v)
			}
		}
	}

	put(withCats)
	put(noCats)
	sch := get()
	if sch["start_hour"].(float64) != 9 {
		t.Errorf("start_hour = %v, want 9", sch["start_hour"])
	}
	catsEqual(sch, []string{"movies"}, []string{"tv"})

	put(clearCats)
	catsEqual(get(), []string{}, []string{})
}

func TestPostIgnoreFNVHash(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)

	body := []byte(`{"url":"https://rutracker.org/forum/viewtopic.php?t=1","name":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/ignore", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePostIgnore(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}
	if len(store.ignore) != 1 {
		t.Fatalf("ignore len = %d", len(store.ignore))
	}
	if store.ignore[0].URLHash == "" {
		t.Error("expected computed url_hash")
	}
}

func TestDeleteIgnoreResetsErrorStreak(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)

	req := httptest.NewRequest(http.MethodDelete, "/api/ignore/abc123", nil)
	req.SetPathValue("hash", "abc123")
	w := httptest.NewRecorder()
	srv.handleDeleteIgnore(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if len(store.resetStreakCalls) != 1 || store.resetStreakCalls[0] != "abc123" {
		t.Fatalf("resetStreakCalls = %v, want [abc123]", store.resetStreakCalls)
	}
}

func TestGetHistory(t *testing.T) {
	recheck := time.Now().Add(2 * time.Hour)
	store := &fakeStore{checkLog: []storage.CheckLogEntry{
		{ID: 1, CheckedAt: time.Now(), URLHash: "abc", ItemName: "n", Status: "error", Error: "boom",
			RecheckAt: &recheck, ForceCheck: true},
	}}
	srv := newTestServer(store)
	req := httptest.NewRequest(http.MethodGet, "/api/history?status=error", nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 || len(resp.Items) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Items[0]["recheck_at"] == "" {
		t.Fatal("expected recheck_at")
	}
	if resp.Items[0]["force_check"] != true {
		t.Fatalf("force_check = %v, want true", resp.Items[0]["force_check"])
	}
}

func TestGetHistoryAbsent(t *testing.T) {
	store := &fakeStore{checkLog: []storage.CheckLogEntry{
		{ID: 1, CheckedAt: time.Now(), URLHash: "a", ItemName: "A", Status: "unchanged"},
		{ID: 2, CheckedAt: time.Now(), URLHash: "b", ItemName: "B", Status: "unchanged"},
	}}
	state := runner.NewState()
	gen := state.Reset()
	state.SetPoller(gen, func(context.Context) (int, error) { return 0, nil })
	state.SetAbsent(gen, map[string]struct{}{"a": {}})
	srv := withTestSession(New(store, func() {}, testEffective, ":0", state, nil))
	w := httptest.NewRecorder()
	srv.handleHistory(w, httptest.NewRequest(http.MethodGet, "/api/history", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(resp.Items))
	}
	if resp.Items[0]["absent"] != true || resp.Items[1]["absent"] != false {
		t.Fatalf("absent = %v, %v, want true, false", resp.Items[0]["absent"], resp.Items[1]["absent"])
	}
}

func TestGetHistorySortRecheck(t *testing.T) {
	soon := time.Now().Add(time.Hour)
	later := time.Now().Add(3 * time.Hour)
	store := &fakeStore{checkLog: []storage.CheckLogEntry{
		{ID: 1, CheckedAt: time.Now(), URLHash: "later", ItemName: "later", Status: "unchanged", RecheckAt: &later},
		{ID: 2, CheckedAt: time.Now().Add(-time.Hour), URLHash: "soon", ItemName: "soon", Status: "unchanged", RecheckAt: &soon},
		{ID: 3, CheckedAt: time.Now().Add(-2 * time.Hour), URLHash: "none", ItemName: "none", Status: "unchanged"},
	}}
	srv := newTestServer(store)
	req := httptest.NewRequest(http.MethodGet, "/api/history?sort=recheck", nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, req)
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(resp.Items))
	}
	if resp.Items[0]["url_hash"] != "soon" || resp.Items[1]["url_hash"] != "later" || resp.Items[2]["url_hash"] != "none" {
		t.Fatalf("sort order = %+v", resp.Items)
	}
}

func TestPutTrackerInvalidType(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)

	body := []byte(`{"hour_limit":"20"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/rutracker", bytes.NewReader(body))
	req.SetPathValue("key", "rutracker")
	w := httptest.NewRecorder()
	srv.handlePutTracker(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if store.settings["trackers"] != nil {
		t.Fatal("trackers should not be saved on type error")
	}
}

func TestPutIntegrationsInvalidType(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)

	body := []byte(`{"flaresolverr":{"timeout_ms":"x"}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/integrations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutIntegrations(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if store.settings["integrations"] != nil {
		t.Fatal("integrations should not be saved on type error")
	}
}

func TestGetTrackersSortedByID(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)
	req := httptest.NewRequest(http.MethodGet, "/api/trackers", nil)
	w := httptest.NewRecorder()
	srv.handleGetTrackers(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp []struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(resp); i++ {
		if resp[i].ID <= resp[i-1].ID {
			t.Fatalf("not sorted by id: %v", resp)
		}
	}
}

func TestPutIntegrationsMerge(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"integrations": json.RawMessage(`{"qbit":{"host":"saved-host","username":"u","password":"p"},"flaresolverr":{"url":"http://flare","timeout_ms":30000},"proxy":{"download_proxy":"http://proxy"}}`),
	}}
	srv := newTestServer(store)

	body := []byte(`{"qbit":{"host":"new-host"}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/integrations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutIntegrations(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	var saved map[string]map[string]any
	if err := json.Unmarshal(store.settings["integrations"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved["qbit"]["host"] != "http://new-host" {
		t.Errorf("qbit host = %v, want http://new-host", saved["qbit"]["host"])
	}
	if saved["qbit"]["username"] != "u" {
		t.Errorf("qbit username = %v, want u", saved["qbit"]["username"])
	}
	if saved["flaresolverr"]["url"] != "http://flare" {
		t.Errorf("flaresolverr url = %v, want http://flare", saved["flaresolverr"]["url"])
	}
	if saved["proxy"]["download_proxy"] != "http://proxy" {
		t.Errorf("proxy = %v, want http://proxy", saved["proxy"]["download_proxy"])
	}
}

func TestPutIntegrationsAutoResolvesChatID(t *testing.T) {
	fakeTelegram := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":[{"message":{"chat":{"id":777}}}]}`))
	}))
	defer fakeTelegram.Close()

	origResolver := telegramChatIDResolver
	defer func() { telegramChatIDResolver = origResolver }()
	var gotToken string
	telegramChatIDResolver = func(_ context.Context, token, _ string) (string, error) {
		gotToken = token
		res, err := http.Get(fakeTelegram.URL)
		if err != nil {
			return "", err
		}
		defer func() { _ = res.Body.Close() }()
		var parsed struct {
			Result []struct {
				Message struct {
					Chat struct {
						ID int `json:"id"`
					} `json:"chat"`
				} `json:"message"`
			} `json:"result"`
		}
		if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
			return "", err
		}
		last := parsed.Result[len(parsed.Result)-1]
		return fmt.Sprintf("%d", last.Message.Chat.ID), nil
	}

	store := &fakeStore{}
	srv := newTestServer(store)
	body := []byte(`{"notifications":{"telegram":{"bot_token":"tok"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/integrations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutIntegrations(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}
	if gotToken != "tok" {
		t.Errorf("resolver called with token = %q, want tok", gotToken)
	}
	var saved struct {
		Notifications struct {
			Telegram struct {
				BotToken string `json:"bot_token"`
				ChatID   string `json:"chat_id"`
			} `json:"telegram"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(store.settings["integrations"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Notifications.Telegram.BotToken != "tok" {
		t.Errorf("bot_token = %q, want tok", saved.Notifications.Telegram.BotToken)
	}
	if saved.Notifications.Telegram.ChatID != "777" {
		t.Errorf("chat_id = %q, want 777", saved.Notifications.Telegram.ChatID)
	}
}

func TestPutIntegrationsChatIDResolveFailureNotSaved(t *testing.T) {
	origResolver := telegramChatIDResolver
	defer func() { telegramChatIDResolver = origResolver }()
	telegramChatIDResolver = func(context.Context, string, string) (string, error) {
		return "", fmt.Errorf("бот не получил ни одного сообщения, напишите ему /start и повторите")
	}

	store := &fakeStore{}
	srv := newTestServer(store)
	body := []byte(`{"notifications":{"telegram":{"bot_token":"tok"}}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/integrations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutIntegrations(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	if store.settings["integrations"] != nil {
		t.Fatal("integrations should not be saved when chat id resolution fails")
	}
}

func TestPostIntegrationsTestNotifyReachesSendAfterResolvingChatID(t *testing.T) {
	origResolver := telegramChatIDResolver
	origSend := sendNotification
	defer func() {
		telegramChatIDResolver = origResolver
		sendNotification = origSend
	}()

	var resolvedToken string
	telegramChatIDResolver = func(_ context.Context, token, _ string) (string, error) {
		resolvedToken = token
		return "555", nil
	}
	var sentChatID string
	sendNotification = func(_ context.Context, cfg notify.Config, _ string, _ *webpush.Store, _ notify.Event) error {
		sentChatID = cfg.TelegramChatID
		return nil
	}

	srv := newTestServer(&fakeStore{})
	body := []byte(`{"kind":"notify","config":{"telegram":{"bot_token":"tok"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleTestIntegration(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var resp testResultResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("ok = false, want true, error: %s", resp.Error)
	}
	if resolvedToken != "tok" {
		t.Errorf("resolver called with token = %q, want tok", resolvedToken)
	}
	if sentChatID != "555" {
		t.Errorf("send reached with chat_id = %q, want 555 (the resolved id)", sentChatID)
	}
}

func TestPostRun(t *testing.T) {
	state := runner.NewState()
	srv := New(&fakeStore{}, func() {}, testEffective, ":0", state, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/run", nil)
	w := httptest.NewRecorder()
	srv.handleRun(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	select {
	case <-state.Trigger():
	default:
		t.Fatal("expected trigger queued")
	}

	gen := state.Reset()
	state.Begin(gen)
	req = httptest.NewRequest(http.MethodPost, "/api/run", nil)
	w = httptest.NewRecorder()
	srv.handleRun(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
}

func TestPostRunAlreadyQueued(t *testing.T) {
	state := runner.NewState()
	srv := New(&fakeStore{}, func() {}, testEffective, ":0", state, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/run", nil)
	w := httptest.NewRecorder()
	srv.handleRun(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/run", nil)
	w = httptest.NewRecorder()
	srv.handleRun(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["error"] != "check already queued" {
		t.Fatalf("error = %q, want check already queued", resp["error"])
	}
}

func TestPostRunDry(t *testing.T) {
	state := runner.NewState()
	srv := New(&fakeStore{}, func() {}, testEffective, ":0", state, nil)

	body := []byte(`{"dry":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleRun(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["started"] != true || resp["dry_run"] != true {
		t.Fatalf("resp = %v", resp)
	}
	select {
	case dry := <-state.Trigger():
		if !dry {
			t.Fatal("expected dry flag on trigger")
		}
	default:
		t.Fatal("expected trigger queued")
	}
}

func TestPostRunRestart(t *testing.T) {
	state := runner.NewState()
	reloaded := false
	srv := New(&fakeStore{}, func() { reloaded = true }, testEffective, ":0", state, nil)
	gen := state.Reset()
	state.Begin(gen)

	body := []byte(`{"restart":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleRun(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["started"] != true || resp["restarted"] != true {
		t.Fatalf("resp = %v", resp)
	}
	if !reloaded {
		t.Fatal("reload not called")
	}
	if !state.ConsumeRestart() {
		t.Fatal("expected restart flag for new runner")
	}
}

func TestPostRunStop(t *testing.T) {
	state := runner.NewState()
	reloaded := false
	srv := New(&fakeStore{}, func() { reloaded = true }, testEffective, ":0", state, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader([]byte(`{"stop":true}`)))
	w := httptest.NewRecorder()
	srv.handleRun(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w.Code)
	}

	if !state.RequestRun() {
		t.Fatal("expected RequestRun to succeed")
	}
	gen := state.Reset()
	state.Begin(gen)
	reloaded = false
	req = httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader([]byte(`{"stop":true}`)))
	w = httptest.NewRecorder()
	srv.handleRun(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["stopped"] != true {
		t.Fatalf("resp = %v", resp)
	}
	if !reloaded {
		t.Fatal("reload not called")
	}
	select {
	case <-state.Trigger():
		t.Fatal("expected trigger drained")
	default:
	}
	if state.ConsumeRestart() {
		t.Fatal("stop must not set restart flag")
	}
}

func TestPutTrackerEnabledPatch(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"domain":"saved.org","enabled":true,"hour_limit":20}}`),
	}}
	srv := newTestServer(store)

	body := []byte(`{"enabled":false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/rutracker", bytes.NewReader(body))
	req.SetPathValue("key", "rutracker")
	w := httptest.NewRecorder()
	srv.handlePutTracker(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	var saved map[string]map[string]any
	if err := json.Unmarshal(store.settings["trackers"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved["rutracker"]["enabled"] != false {
		t.Errorf("enabled = %v, want false", saved["rutracker"]["enabled"])
	}
	if saved["rutracker"]["domain"] != "saved.org" {
		t.Errorf("domain = %v, want saved.org", saved["rutracker"]["domain"])
	}
	if saved["rutracker"]["hour_limit"].(float64) != 20 {
		t.Errorf("hour_limit = %v, want 20", saved["rutracker"]["hour_limit"])
	}
}

func TestPostPlanForce(t *testing.T) {
	store := &fakeStore{forceItems: map[string]bool{"abc123": false}}
	srv := newTestServer(store)

	body := []byte(`{"force":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/plan/abc123/force", bytes.NewReader(body))
	req.SetPathValue("hash", "abc123")
	w := httptest.NewRecorder()
	srv.handlePlanForce(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp["force"] {
		t.Fatal("expected force true in response")
	}
	if !resp["force_queued"] {
		t.Fatal("expected force_queued true when the runner queue has room")
	}
	if !store.forceItems["abc123"] {
		t.Fatal("expected force flag set in store")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/plan/missing/force", bytes.NewReader(body))
	req.SetPathValue("hash", "missing")
	w = httptest.NewRecorder()
	srv.handlePlanForce(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestPostPlanForceQueueFull(t *testing.T) {
	store := &fakeStore{forceItems: map[string]bool{"abc123": false}}
	state := runner.NewState()
	srv := New(store, func() {}, testEffective, ":0", state, nil)

	// забиваем очередь форса до false, чтобы не зашивать сюда приватную емкость пакета
	for state.RequestForce("filler") {
	}

	body := []byte(`{"force":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/plan/abc123/force", bytes.NewReader(body))
	req.SetPathValue("hash", "abc123")
	w := httptest.NewRecorder()
	srv.handlePlanForce(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["force_queued"] {
		t.Fatal("expected force_queued false when the runner queue is full")
	}
	if !store.forceItems["abc123"] {
		t.Fatal("expected force flag set in store even though queueing failed")
	}
}

func TestPostPlanForceAll(t *testing.T) {
	store := &fakeStore{
		forceItems: map[string]bool{
			"h1": false,
			"h2": true,
			"h3": false,
		},
		ignore: []storage.IgnoreEntry{{URLHash: "h3"}},
	}
	srv := newTestServer(store)

	req := httptest.NewRequest(http.MethodPost, "/api/plan/force-all", nil)
	w := httptest.NewRecorder()
	srv.handlePlanForceAll(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Updated int64 `json:"updated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	// h2 уже был форсирован и в счет измененных не идет
	if resp.Updated != 1 {
		t.Fatalf("updated = %d, want 1", resp.Updated)
	}
	if !store.forceItems["h1"] || !store.forceItems["h2"] {
		t.Fatal("expected h1 and h2 forced")
	}
	if store.forceItems["h3"] {
		t.Fatal("expected h3 unchanged (ignored)")
	}
}

func TestPostPlanForceAllClear(t *testing.T) {
	store := &fakeStore{
		forceItems: map[string]bool{
			"h1": true,
			"h2": true,
			"h3": true,
		},
		ignore: []storage.IgnoreEntry{{URLHash: "h3"}},
	}
	srv := newTestServer(store)

	body := []byte(`{"force":false}`)
	req := httptest.NewRequest(http.MethodPost, "/api/plan/force-all", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePlanForceAll(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Updated int64 `json:"updated"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Updated != 3 {
		t.Fatalf("updated = %d, want 3", resp.Updated)
	}
	if store.forceItems["h1"] || store.forceItems["h2"] || store.forceItems["h3"] {
		t.Fatal("expected all force flags cleared")
	}
}

func TestPutTrackerActiveDomainMerge(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"domain":"saved.org","enabled":true}}`),
	}}
	srv := newTestServer(store)

	body := []byte(`{"active_domain":"mirror.example"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/rutracker", bytes.NewReader(body))
	req.SetPathValue("key", "rutracker")
	w := httptest.NewRecorder()
	srv.handlePutTracker(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	var saved map[string]map[string]any
	if err := json.Unmarshal(store.settings["trackers"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved["rutracker"]["active_domain"] != "mirror.example" {
		t.Errorf("active_domain = %v, want mirror.example", saved["rutracker"]["active_domain"])
	}
	if saved["rutracker"]["domain"] != "saved.org" {
		t.Errorf("domain = %v, want saved.org", saved["rutracker"]["domain"])
	}
}

func TestGetStatusRunning(t *testing.T) {
	state := runner.NewState()
	srv := New(&fakeStore{}, func() {}, testEffective, ":0", state, nil)
	gen := state.Reset()
	state.Begin(gen)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.handleStatus(w, req)
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if running, _ := resp["running"].(bool); !running {
		t.Fatalf("running = %v, want true", resp["running"])
	}
}

func TestGetStatusRedownloadByClient(t *testing.T) {
	cases := []struct {
		client string
		want   bool
	}{
		{"", true},
		{"qbit", true},
		{" QBIT ", true},
		{"transmission", false},
		{"Deluge", false},
		{"DownloadStation", false},
	}
	for _, tc := range cases {
		t.Run(tc.client, func(t *testing.T) {
			effective := func() config.App {
				cfg := testEffective()
				cfg.Manager.Client = tc.client
				return cfg
			}
			srv := New(&fakeStore{}, func() {}, effective, ":0", runner.NewState(), nil)
			req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
			w := httptest.NewRecorder()
			srv.handleStatus(w, req)
			var resp map[string]any
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatal(err)
			}
			got, ok := resp["redownload"].(bool)
			if !ok || got != tc.want {
				t.Fatalf("redownload = %v, want %v", resp["redownload"], tc.want)
			}
		})
	}
}

func TestPutIntegrationsInvalidProxyScheme(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)
	body := []byte(`{"proxy":{"download_proxy":"ftp://bad"}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/integrations", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutIntegrations(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestGetLogs(t *testing.T) {
	logger.Info("log line one")
	logger.Warn("log line two")
	srv := newTestServer(&fakeStore{})
	req := httptest.NewRequest(http.MethodGet, "/api/logs?limit=10", nil)
	w := httptest.NewRecorder()
	srv.handleLogs(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Items []struct {
			Time    string `json:"time"`
			Level   string `json:"level"`
			Message string `json:"message"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) < 2 {
		t.Fatalf("expected >=2 log items, got %d", len(resp.Items))
	}
	last := resp.Items[len(resp.Items)-1]
	if last.Level != "WARN" || last.Message != "log line two" {
		t.Fatalf("last item = %+v", last)
	}
}

func TestGetStatusAlerts(t *testing.T) {
	reg := breakeralerts.New()
	reg.Open("kinozal", "circuit open")
	srv := New(&fakeStore{}, func() {}, testEffective, ":0", runner.NewState(), reg)
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	srv.handleStatus(w, req)
	var resp struct {
		Alerts []struct {
			Tracker string `json:"tracker"`
			Since   string `json:"since"`
			Error   string `json:"error"`
		} `json:"alerts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Alerts) != 1 || resp.Alerts[0].Tracker != "kinozal" {
		t.Fatalf("alerts = %+v", resp.Alerts)
	}
}

func TestPostTrackerTestUnknownKey(t *testing.T) {
	srv := newTestServer(&fakeStore{})
	req := httptest.NewRequest(http.MethodPost, "/api/trackers/nope/test", nil)
	req.SetPathValue("key", "nope")
	w := httptest.NewRecorder()
	srv.handleTestTracker(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestGetHistoryDedupesByName(t *testing.T) {
	now := time.Now()
	store := &fakeStore{checkLog: []storage.CheckLogEntry{
		{ID: 1, CheckedAt: now.Add(-time.Hour), URLHash: "old", ItemName: "Same", Status: "unchanged"},
		{ID: 2, CheckedAt: now, URLHash: "new", ItemName: " Same ", Status: "updated"},
		{ID: 3, CheckedAt: now, URLHash: "other", ItemName: "Other", Status: "unchanged"},
	}}
	srv := newTestServer(store)
	req := httptest.NewRequest(http.MethodGet, "/api/history", nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, req)
	var resp struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
	if resp.Items[0]["url_hash"] != "new" {
		t.Fatalf("items = %+v, want latest check for duplicate name", resp.Items)
	}
}

func TestGetHistoryNameFilter(t *testing.T) {
	store := &fakeStore{checkLog: []storage.CheckLogEntry{
		{ID: 1, URLHash: "h1", ItemName: "Breaking Bad S01", Status: "unchanged"},
		{ID: 2, URLHash: "h2", ItemName: "Better Call Saul", Status: "unchanged"},
		{ID: 3, URLHash: "h3", ItemName: "bad torrent", Status: "error"},
	}}
	srv := newTestServer(store)
	req := httptest.NewRequest(http.MethodGet, "/api/history?name=bad", nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 2 || len(resp.Items) != 2 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestGetHistoryIgnoredOnly(t *testing.T) {
	store := &fakeStore{
		checkLog: []storage.CheckLogEntry{
			{ID: 1, URLHash: "h1", ItemName: "visible", Status: "unchanged"},
			{ID: 2, URLHash: "h2", ItemName: "ignored", Status: "error"},
		},
		ignore: []storage.IgnoreEntry{{URLHash: "h2"}},
	}
	srv := newTestServer(store)
	req := httptest.NewRequest(http.MethodGet, "/api/history?ignored=1&status=error", nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Total != 1 || len(resp.Items) != 1 || resp.Items[0]["url_hash"] != "h2" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestTrackerTestProxyTimeoutFromConfig(t *testing.T) {
	cfg := testEffective()
	cfg.Tracker["kinozal"] = &config.TrackerItem{Name: "kinozal", ProxyType: 0}
	patch := json.RawMessage(`{"proxy_type":1}`)
	if got := effectiveTrackerProxyType(cfg, "kinozal", patch); got != 1 {
		t.Fatalf("proxy_type = %d, want 1", got)
	}
}

type capturingStore struct {
	fakeStore
	lastFilter storage.CheckLogFilter
}

func (c *capturingStore) ListCheckLog(ctx context.Context, filter storage.CheckLogFilter) ([]storage.CheckLogEntry, int, error) {
	c.lastFilter = filter
	return c.fakeStore.ListCheckLog(ctx, filter)
}

func TestGetHistoryLimitCap(t *testing.T) {
	store := &capturingStore{fakeStore: fakeStore{checkLog: []storage.CheckLogEntry{{ID: 1}}}}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/history?limit=100000", nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, req)
	if store.lastFilter.Limit != 1000 {
		t.Fatalf("limit = %d, want 1000", store.lastFilter.Limit)
	}
}

func TestGetHistoryTrackerFilter(t *testing.T) {
	store := &capturingStore{fakeStore: fakeStore{checkLog: []storage.CheckLogEntry{{ID: 1}}}}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/history?tracker=5", nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, req)
	if store.lastFilter.TrackerID != 5 {
		t.Fatalf("tracker id = %d, want 5", store.lastFilter.TrackerID)
	}

	for _, v := range []string{"abc", "-1", "0"} {
		req = httptest.NewRequest(http.MethodGet, "/api/history?tracker="+v, nil)
		w = httptest.NewRecorder()
		srv.handleHistory(w, req)
		if store.lastFilter.TrackerID != 0 {
			t.Fatalf("tracker=%s: tracker id = %d, want 0", v, store.lastFilter.TrackerID)
		}
	}
}

func TestGetHistoryCategoryFilter(t *testing.T) {
	store := &capturingStore{fakeStore: fakeStore{checkLog: []storage.CheckLogEntry{{ID: 1}}}}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)

	req := httptest.NewRequest(http.MethodGet, "/api/history?category=movies", nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, req)
	if store.lastFilter.Category != "movies" {
		t.Fatalf("category = %q, want movies", store.lastFilter.Category)
	}

	long := strings.Repeat("x", 250)
	req = httptest.NewRequest(http.MethodGet, "/api/history?category="+long, nil)
	w = httptest.NewRecorder()
	srv.handleHistory(w, req)
	if len(store.lastFilter.Category) != 200 {
		t.Fatalf("category len = %d, want 200", len(store.lastFilter.Category))
	}
}

func TestGetHistoryPlanViewExcludesIgnored(t *testing.T) {
	store := &capturingStore{fakeStore: fakeStore{checkLog: []storage.CheckLogEntry{{ID: 1}}}}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)

	cases := []struct {
		query string
		want  bool
	}{
		{"sort=recheck", true},
		{"sort=checked", false},
		{"sort=recheck&ignored=1", false},
	}
	for _, tt := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/history?"+tt.query, nil)
		w := httptest.NewRecorder()
		srv.handleHistory(w, req)
		if store.lastFilter.ExcludeIgnored != tt.want {
			t.Fatalf("query=%s: ExcludeIgnored = %v, want %v", tt.query, store.lastFilter.ExcludeIgnored, tt.want)
		}
	}
}

func TestGetHistorySummary(t *testing.T) {
	store := &fakeStore{summary: storage.CheckLogSummary{Updated: 2, Errors: 2}}
	srv := newTestServer(store)
	req := httptest.NewRequest(http.MethodGet, "/api/history/summary", nil)
	w := httptest.NewRecorder()
	srv.handleHistorySummary(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp map[string]int
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"updated": 2, "errors": 2}
	for k, v := range want {
		if resp[k] != v {
			t.Fatalf("%s = %d, want %d", k, resp[k], v)
		}
	}
}

func TestHistoryRoutesDoNotConflict(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()

	testURL := "https://rutracker.org/forum/viewtopic.php?t=999"
	testURLHash := storagePkg.URLHash(testURL)
	entries := []*model.Entry{{URL: testURL, Hash: "abc999", Name: "Route Test"}}
	managerClientFactory = func(_ string, _ model.ManagerConfig) (managerClient, error) {
		return &fakeQbitClient{entries: entries}, nil
	}

	store := &fakeStore{summary: storage.CheckLogSummary{Updated: 4}}
	srv := newTestServer(store)
	handler := srv.srv.Handler

	req := httptest.NewRequest(http.MethodGet, "/api/history/summary", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("summary status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var sum map[string]int
	if err := json.NewDecoder(w.Body).Decode(&sum); err != nil {
		t.Fatal(err)
	}
	if sum["updated"] != 4 {
		t.Fatalf("updated = %d, want 4", sum["updated"])
	}

	req = httptest.NewRequest(http.MethodPost, "/api/history/"+testURLHash+"/redownload", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("redownload status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var rd map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&rd); err != nil {
		t.Fatal(err)
	}
	if !rd["ok"] {
		t.Fatal("redownload ok = false, want true")
	}
}

func TestPostIntegrationsTestNotifyWithConfig(t *testing.T) {
	hit := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	srv := newTestServer(&fakeStore{})
	body := []byte(`{"kind":"notify","config":{"webhook":{"url":"` + ts.URL + `"}}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleTestIntegration(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var resp testResultResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("ok = false, want true, error: %s", resp.Error)
	}
	if !hit {
		t.Fatal("expected webhook to receive a request")
	}
}

func TestPostIntegrationsTestNotifyNoTransport(t *testing.T) {
	srv := newTestServer(&fakeStore{})
	body := []byte(`{"kind":"notify"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleTestIntegration(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp testResultResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("ok = true, want false")
	}
	if resp.Error == "" {
		t.Fatal("expected error message")
	}
}

func TestRequestBodyTooLarge(t *testing.T) {
	srv := newTestServer(&fakeStore{})
	body := make([]byte, maxRequestBody+1)
	req := httptest.NewRequest(http.MethodPut, "/api/schedule", bytes.NewReader(body))
	w := httptest.NewRecorder()
	withMaxBody(srv.handlePutSchedule)(w, req)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 400 or 413", w.Code)
	}
}

func TestHealthzSuccess(t *testing.T) {
	store := &fakeStore{pingErr: nil}
	srv := newTestServer(store)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.handleHealthz(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "ok" {
		t.Fatalf("status = %q, want ok", resp["status"])
	}
}

func TestHealthzAlwaysOK(t *testing.T) {
	srv := newTestServer(&fakeStore{pingErr: fmt.Errorf("connection failed")})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.handleHealthz(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestReadyz(t *testing.T) {
	srv := newTestServer(&fakeStore{})
	w := httptest.NewRecorder()
	srv.handleReadyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("ok case: %d %q", w.Code, w.Body.String())
	}

	srv = newTestServer(&fakeStore{pingErr: fmt.Errorf("connection failed")})
	w = httptest.NewRecorder()
	srv.handleReadyz(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != `{"status":"error"}` {
		t.Fatalf("body = %q, must carry no error text", w.Body.String())
	}
}

func TestProbesAndMetricsBypassSessionAuth(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	base := srv.effective
	srv.effective = func() config.App {
		cfg := base()
		cfg.Metrics.Token = "tok"
		return cfg
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "203.0.113.5:1234"
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s without cookie: %d", path, w.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/metrics with bearer and no cookie: %d", w.Code)
	}
}

func TestMetricsGate(t *testing.T) {
	reg := breakeralerts.New()
	reg.Open("rutracker", "circuit open")
	srv := New(&fakeStore{}, func() {}, func() config.App { return config.App{} }, ":0", runner.NewState(), reg)
	do := func(auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.RemoteAddr = "192.168.1.5:1234"
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		return w
	}

	if w := do("Bearer anything"); w.Code != http.StatusNotFound {
		t.Fatalf("no token configured: %d, want 404", w.Code)
	}

	srv.effective = func() config.App {
		return config.App{
			Metrics: config.Metrics{Token: "s3cret"},
			Tracker: config.Tracker{
				"rutracker": {Enabled: true},
				"kinozal":   {Enabled: true},
				"off":       {Enabled: false},
			},
		}
	}
	for _, auth := range []string{"", "Bearer wrong", "Basic s3cret", "s3cret"} {
		w := do(auth)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("auth %q: %d, want 401", auth, w.Code)
		}
		if got := w.Header().Get("WWW-Authenticate"); got != "Bearer" {
			t.Fatalf("auth %q: WWW-Authenticate = %q", auth, got)
		}
	}

	w := do("Bearer s3cret")
	if w.Code != http.StatusOK {
		t.Fatalf("right token: %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Fatalf("content type = %q", ct)
	}
	body := w.Body.String()
	for _, want := range []string{
		"# TYPE ripperr_checks_total counter",
		"# TYPE ripperr_notifications_total counter",
		"# TYPE ripperr_tracker_circuit_open gauge",
		`ripperr_tracker_circuit_open{tracker="kinozal"} 0`,
		`ripperr_tracker_circuit_open{tracker="rutracker"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body misses %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, `tracker="off"`) {
		t.Fatalf("disabled tracker exported:\n%s", body)
	}
}

func TestHandleRedownload(t *testing.T) {
	fakeQbitFactory := func(entries []*model.Entry, forceRedownloadErr error) func(string, model.ManagerConfig) (managerClient, error) {
		return func(_ string, _ model.ManagerConfig) (managerClient, error) {
			return &fakeQbitClient{
				entries:            entries,
				forceRedownloadErr: forceRedownloadErr,
			}, nil
		}
	}

	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()

	store := &fakeStore{}
	srv := newTestServer(store)

	testURL := "https://rutracker.org/forum/viewtopic.php?t=123"
	testURLHash := storagePkg.URLHash(testURL)
	entries := []*model.Entry{
		{
			URL:  testURL,
			Hash: "abc123",
			Name: "Test Torrent",
		},
	}

	managerClientFactory = fakeQbitFactory(entries, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/history/"+testURLHash+"/redownload", nil)
	req.SetPathValue("hash", testURLHash)
	w := httptest.NewRecorder()
	srv.handleRedownload(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp["ok"] {
		t.Fatal("ok should be true")
	}

	managerClientFactory = fakeQbitFactory(nil, nil)
	req = httptest.NewRequest(http.MethodPost, "/api/history/"+testURLHash+"/redownload", nil)
	req.SetPathValue("hash", testURLHash)
	w = httptest.NewRecorder()
	srv.handleRedownload(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp["error"] != "torrent not found in the download client" {
		t.Fatalf("error = %q, want torrent not found in the download client", errResp["error"])
	}

	entries = []*model.Entry{
		{
			URL:  testURL,
			Hash: "abc123",
			Name: "Test Torrent",
		},
	}
	managerClientFactory = fakeQbitFactory(entries, fmt.Errorf("redownload failed"))
	req = httptest.NewRequest(http.MethodPost, "/api/history/"+testURLHash+"/redownload", nil)
	req.SetPathValue("hash", testURLHash)
	w = httptest.NewRecorder()
	srv.handleRedownload(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body: %s", w.Code, w.Body.String())
	}
}

func TestHandleRedownloadTransmissionUnsupportedReturns501(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()

	testURL := "https://rutracker.org/forum/viewtopic.php?t=456"
	testURLHash := storagePkg.URLHash(testURL)
	entries := []*model.Entry{{URL: testURL, Hash: "abc456", Name: "Transmission Torrent"}}
	managerClientFactory = func(_ string, _ model.ManagerConfig) (managerClient, error) {
		return &fakeQbitClient{entries: entries, forceRedownloadErr: transmission.ErrRedownloadUnsupported}, nil
	}

	store := &fakeStore{}
	effective := func() config.App {
		cfg := testEffective()
		cfg.Manager.Client = "transmission"
		return cfg
	}
	srv := New(store, func() {}, effective, ":0", runner.NewState(), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/history/"+testURLHash+"/redownload", nil)
	req.SetPathValue("hash", testURLHash)
	w := httptest.NewRecorder()
	srv.handleRedownload(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501, body: %s", w.Code, w.Body.String())
	}
	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp["error"] != transmission.ErrRedownloadUnsupported.Error() {
		t.Fatalf("error = %q, want %q", errResp["error"], transmission.ErrRedownloadUnsupported.Error())
	}
}

func TestHandleRedownloadTransmissionSkipsSemaphore(t *testing.T) {
	// 501 для Transmission уходит до попытки занять redownloadSlot, иначе вызов
	// висел бы на занятом слоте до пятиминутного таймаута вместо мгновенного ответа
	redownloadSlot <- struct{}{}
	defer func() { <-redownloadSlot }()

	store := &fakeStore{}
	effective := func() config.App {
		cfg := testEffective()
		// регистр и пробелы как у реального MANAGER_CLIENT из env
		cfg.Manager.Client = " Transmission "
		return cfg
	}
	srv := New(store, func() {}, effective, ":0", runner.NewState(), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/history/somehash/redownload", nil)
	req.SetPathValue("hash", "somehash")
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.handleRedownload(w, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleRedownload blocked on the redownload semaphore instead of returning 501 early")
	}

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501, body: %s", w.Code, w.Body.String())
	}
}

func TestHandleTestIntegrationUsesClientSpecificConfigWhenTransmissionActive(t *testing.T) {
	store := &fakeStore{}
	effective := func() config.App {
		cfg := testEffective()
		cfg.Manager.Client = "transmission"
		cfg.Manager.QbitTorrent.Host = "http://127.0.0.1:1"
		cfg.Manager.QbitTorrent.Username = "u"
		cfg.Manager.QbitTorrent.Password = "p"
		cfg.Manager.Transmission.Host = "http://127.0.0.1:2"
		return cfg
	}
	srv := New(store, func() {}, effective, ":0", runner.NewState(), nil)

	// kind=qbit ходит на qbit-хост, даже когда активен transmission
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader([]byte(`{"kind":"qbit"}`)))
	w := httptest.NewRecorder()
	srv.handleTestIntegration(w, req)
	var resp testResultResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK || !strings.Contains(resp.Error, "127.0.0.1:1") {
		t.Fatalf("qbit test error = %q, want dial error mentioning 127.0.0.1:1", resp.Error)
	}

	// и наоборот
	req = httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader([]byte(`{"kind":"transmission"}`)))
	w = httptest.NewRecorder()
	srv.handleTestIntegration(w, req)
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK || !strings.Contains(resp.Error, "127.0.0.1:2") {
		t.Fatalf("transmission test error = %q, want dial error mentioning 127.0.0.1:2", resp.Error)
	}
}

func TestPostRunStopBlocksFurtherRuns(t *testing.T) {
	state := runner.NewState()
	srv := New(&fakeStore{}, func() {}, testEffective, ":0", state, nil)
	gen := state.Reset()
	state.Begin(gen)

	req := httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader([]byte(`{"stop":true}`)))
	w := httptest.NewRecorder()
	srv.handleRun(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("stop status = %d, want 202", w.Code)
	}
	if !state.Stopping() {
		t.Fatal("expected stopping flag after stop")
	}

	for _, body := range []string{`{"stop":true}`, `{"restart":true}`, `{"dry":true}`, `{}`} {
		req = httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader([]byte(body)))
		w = httptest.NewRecorder()
		srv.handleRun(w, req)
		if w.Code != http.StatusConflict {
			t.Fatalf("body %s: status = %d, want 409", body, w.Code)
		}
	}

	state.Reset()
	req = httptest.NewRequest(http.MethodPost, "/api/run", nil)
	w = httptest.NewRecorder()
	srv.handleRun(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("after reset status = %d, want 202", w.Code)
	}
}

func TestHandleRedownloadSerializes(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()

	testURL := "https://rutracker.org/forum/viewtopic.php?t=777"
	testURLHash := storagePkg.URLHash(testURL)
	entries := []*model.Entry{{URL: testURL, Hash: "abc777", Name: "Serialized"}}
	tracker := &concurrencyTracker{}

	managerClientFactory = func(_ string, _ model.ManagerConfig) (managerClient, error) {
		return &fakeQbitClient{
			entries:           entries,
			redownloadDelay:   50 * time.Millisecond,
			redownloadTracker: tracker,
		}, nil
	}

	srv := newTestServer(&fakeStore{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/history/"+testURLHash+"/redownload", nil)
			req.SetPathValue("hash", testURLHash)
			w := httptest.NewRecorder()
			srv.handleRedownload(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200, body: %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()

	if peak := tracker.peak(); peak != 1 {
		t.Fatalf("peak concurrency = %d, want 1", peak)
	}
}

func TestPostPlanForceAllPassesListFilters(t *testing.T) {
	store := &fakeStore{forceItems: map[string]bool{"h1": false}}
	srv := newTestServer(store)

	req := httptest.NewRequest(http.MethodPost, "/api/plan/force-all?sort=recheck&tracker=3&category=sonarr&name=from&status=error", nil)
	w := httptest.NewRecorder()
	srv.handlePlanForceAll(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	got := store.forceAllFilter
	if got.TrackerID != 3 || got.Category != "sonarr" || got.Name != "from" || got.Status != "error" {
		t.Fatalf("filter = %+v, want tracker 3, category sonarr, name from, status error", got)
	}
	if got.SortBy != "recheck" || !got.ExcludeIgnored {
		t.Fatalf("filter = %+v, want sort recheck with ignored excluded", got)
	}
	if got.Limit != 0 || got.Offset != 0 {
		t.Fatalf("filter = %+v, want no pagination", got)
	}
}

func TestPostPlanForceAllKeepsIgnoredWhenListShowsThem(t *testing.T) {
	store := &fakeStore{forceItems: map[string]bool{"h1": false}}
	srv := newTestServer(store)

	req := httptest.NewRequest(http.MethodPost, "/api/plan/force-all?ignored=1", nil)
	w := httptest.NewRecorder()
	srv.handlePlanForceAll(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if got := store.forceAllFilter; !got.IgnoredOnly || got.ExcludeIgnored {
		t.Fatalf("filter = %+v, want ignored-only without exclusion", got)
	}
}

func TestPostPlanForceAllQueuesCheckNow(t *testing.T) {
	store := &fakeStore{forceItems: map[string]bool{"h1": false, "h2": false}}
	srv := newTestServer(store)

	req := httptest.NewRequest(http.MethodPost, "/api/plan/force-all", nil)
	w := httptest.NewRecorder()
	srv.handlePlanForceAll(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	queued := srv.runnerState.DrainForce()
	if len(queued) != 2 {
		t.Fatalf("queued = %v, want both forced releases", queued)
	}
}

func TestPostPlanForceAllClearQueuesNothing(t *testing.T) {
	store := &fakeStore{forceItems: map[string]bool{"h1": true}}
	srv := newTestServer(store)

	req := httptest.NewRequest(http.MethodPost, "/api/plan/force-all", bytes.NewReader([]byte(`{"force":false}`)))
	w := httptest.NewRecorder()
	srv.handlePlanForceAll(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if queued := srv.runnerState.DrainForce(); len(queued) != 0 {
		t.Fatalf("queued = %v, want nothing on clear", queued)
	}
}

func TestGetTrackersSplitsLoginAndPassword(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"credentials":"login=%D0%B2%D1%85%D0%BE%D0%B4&login_password=s3cret&login_username=vasya"}}`),
	}}
	srv := New(store, func() {}, func() config.App {
		cfg := testEffective()
		cfg.Tracker = registry.ConfigDefaults()
		return cfg
	}, ":0", runner.NewState(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/trackers", nil)
	w := httptest.NewRecorder()
	srv.handleGetTrackers(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp []struct {
		Key           string `json:"key"`
		Login         string `json:"login"`
		Password      string `json:"password"`
		Credentials   string `json:"credentials"`
		SupportsLogin bool   `json:"supports_login"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	var rut, nnm = -1, -1
	for i, item := range resp {
		switch item.Key {
		case "rutracker":
			rut = i
		case "nnm":
			nnm = i
		}
	}
	if rut < 0 || nnm < 0 {
		t.Fatalf("rutracker or nnm missing from %v", resp)
	}
	if !resp[rut].SupportsLogin {
		t.Error("rutracker must report supports_login")
	}
	if resp[rut].Login != "vasya" || resp[rut].Password != settings.SecretMask {
		t.Errorf("login/password = %q/%q, want vasya/%s", resp[rut].Login, resp[rut].Password, settings.SecretMask)
	}
	if want := "login=%D0%B2%D1%85%D0%BE%D0%B4&login_password={password}&login_username={login}"; resp[rut].Credentials != want {
		t.Errorf("credentials = %q, want template %q", resp[rut].Credentials, want)
	}
	// у nnm вход только по кукам, полей логина нет
	if resp[nnm].SupportsLogin || resp[nnm].Login != "" || resp[nnm].Password != "" {
		t.Errorf("nnm = %+v, want no login form", resp[nnm])
	}
}

func TestPutTrackerLoginKeepsOtherCredentialFields(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"credentials":"login=%D0%B2%D1%85%D0%BE%D0%B4&login_password=old&login_username=old"}}`),
	}}
	srv := newTrackerTestServer(store, "rutracker", "login=%D0%B2%D1%85%D0%BE%D0%B4&login_password=old&login_username=old")

	body := []byte(`{"login":"vasya","password":"new pass"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/rutracker", bytes.NewReader(body))
	req.SetPathValue("key", "rutracker")
	w := httptest.NewRecorder()
	srv.handlePutTracker(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	assertStoredTemplate(t, store, "rutracker",
		"login=%D0%B2%D1%85%D0%BE%D0%B4&login_password={password}&login_username={login}",
		"login=%D0%B2%D1%85%D0%BE%D0%B4&login_password=new+pass&login_username=vasya")
	creds, login, _ := storedTrackerLogin(t, store, "rutracker")
	if login != "vasya" || strings.Contains(creds, "vasya") {
		t.Errorf("login = %q, credentials = %q, want login only in its own field", login, creds)
	}
}

func TestPutTrackerLoginOnEmptyCredentialsUsesTemplate(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)

	body := []byte(`{"login":"vasya","password":"pw"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/kinozal", bytes.NewReader(body))
	req.SetPathValue("key", "kinozal")
	w := httptest.NewRecorder()
	srv.handlePutTracker(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	assertStoredTemplate(t, store, "kinozal", "username={login}&password={password}&to=%2F", "username=vasya&password=pw&to=%2F")
}

func TestPutTrackerRawCredentialsAreTheBaseForLogin(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"credentials":"login_username=old&login_password=old"}}`),
	}}
	srv := newTrackerTestServer(store, "rutracker", "login_username=old&login_password=old")

	body := []byte(`{"credentials":"login_username=&login_password=&extra=1","login":"vasya","password":"pw"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/rutracker", bytes.NewReader(body))
	req.SetPathValue("key", "rutracker")
	w := httptest.NewRecorder()
	srv.handlePutTracker(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	assertStoredTemplate(t, store, "rutracker",
		"login_username={login}&login_password={password}&extra=1",
		"login_username=vasya&login_password=pw&extra=1")
}

func TestPutTrackerWithoutLoginFieldsLeavesCredentialsAlone(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"credentials":"login_username=vasya&login_password=pw"}}`),
	}}
	srv := newTrackerTestServer(store, "rutracker", "login_username=vasya&login_password=pw")

	body := []byte(`{"enabled":false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/rutracker", bytes.NewReader(body))
	req.SetPathValue("key", "rutracker")
	w := httptest.NewRecorder()
	srv.handlePutTracker(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	var saved map[string]map[string]any
	if err := json.Unmarshal(store.settings["trackers"], &saved); err != nil {
		t.Fatal(err)
	}
	if got, _ := saved["rutracker"]["credentials"].(string); got != "login_username=vasya&login_password=pw" {
		t.Fatalf("credentials = %q, want untouched", got)
	}
}

func TestPutTrackerLoginIgnoredWhenTrackerHasNoForm(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)

	body := []byte(`{"login":"vasya","password":"pw"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/nnm", bytes.NewReader(body))
	req.SetPathValue("key", "nnm")
	w := httptest.NewRecorder()
	srv.handlePutTracker(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	var saved map[string]map[string]any
	if err := json.Unmarshal(store.settings["trackers"], &saved); err != nil {
		t.Fatal(err)
	}
	if _, ok := saved["nnm"]["credentials"]; ok {
		t.Errorf("credentials must stay unset, got %v", saved["nnm"]["credentials"])
	}
}

func newTrackerTestServer(store *fakeStore, key, credentials string) *Server {
	return New(store, func() {}, func() config.App {
		cfg := testEffective()
		cfg.Tracker = registry.ConfigDefaults()
		// в бою effective() уже с оверлеем из БД, одного fakeStore тут мало
		if it := cfg.Tracker.ByKey(key); it != nil {
			it.Credentials = credentials
		}
		return cfg
	}, ":0", runner.NewState(), nil)
}

func TestApplyTrackerLoginIgnoresJSONNull(t *testing.T) {
	cfg := testEffective()
	cfg.Tracker = registry.ConfigDefaults()
	cfg.Tracker.ByKey("rutracker").Credentials = "login_username=vasya&login_password=pw"

	out, err := applyTrackerLogin(cfg, "rutracker", json.RawMessage(`{"login":null,"password":null}`))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["credentials"]; ok {
		t.Fatalf("credentials must stay untouched, got %v", body["credentials"])
	}
	if _, ok := body["login"]; ok {
		t.Error("login must be stripped from the patch")
	}
}

func TestApplyTrackerLoginAcceptsEmptyStringAsErase(t *testing.T) {
	cfg := testEffective()
	cfg.Tracker = registry.ConfigDefaults()
	cfg.Tracker.ByKey("rutracker").Credentials = "login_username=vasya&login_password=pw"

	out, err := applyTrackerLogin(cfg, "rutracker", json.RawMessage(`{"password":""}`))
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{settings: map[string]json.RawMessage{"trackers": json.RawMessage(`{"rutracker":` + string(out) + `}`)}}
	assertStoredTemplate(t, store, "rutracker",
		"login_username={login}&login_password={password}",
		"login_username=vasya&login_password=")
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	if password, ok := body["password"]; !ok || password != "" {
		t.Fatalf("password = %v (sent %v), want the password cleared", password, ok)
	}
}

func TestPostIntegrationsTestNotifyKeepsSavedChatID(t *testing.T) {
	origResolver := telegramChatIDResolver
	origSend := sendNotification
	defer func() {
		telegramChatIDResolver = origResolver
		sendNotification = origSend
	}()

	var resolved bool
	telegramChatIDResolver = func(context.Context, string, string) (string, error) {
		resolved = true
		return "999", nil
	}
	var sentChatID string
	sendNotification = func(_ context.Context, cfg notify.Config, _ string, _ *webpush.Store, _ notify.Event) error {
		sentChatID = cfg.TelegramChatID
		return nil
	}

	// форма шлет только токен, и сохраненный chat id должен пережить проверку.
	// Иначе на живом боте getUpdates отвечает, что сообщений не было
	effective := func() config.App {
		cfg := testEffective()
		cfg.Notify.TelegramToken = "tok"
		cfg.Notify.TelegramChatID = "180688465"
		return cfg
	}
	srv := New(&fakeStore{}, func() {}, effective, ":0", runner.NewState(), nil)
	body := []byte(`{"kind":"notify","config":{"telegram":{"bot_token":"tok"},"webhook":{"url":""},"enabled":true}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleTestIntegration(w, req)

	var resp testResultResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("ok = false, error: %s", resp.Error)
	}
	if resolved {
		t.Error("resolver called although chat id is already known")
	}
	if sentChatID != "180688465" {
		t.Errorf("send reached with chat_id = %q, want the saved one", sentChatID)
	}

	// новый токен: старый chat id принадлежит другому боту, его надо вычислить заново
	resolved = false
	body = []byte(`{"kind":"notify","config":{"telegram":{"bot_token":"other"},"webhook":{"url":""},"enabled":true}}`)
	req = httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader(body))
	w = httptest.NewRecorder()
	srv.handleTestIntegration(w, req)
	if !resolved {
		t.Error("resolver not called after the token changed")
	}
	if sentChatID != "999" {
		t.Errorf("send reached with chat_id = %q, want 999", sentChatID)
	}
}

func TestPutSchedulePurgesDisabledCategories(t *testing.T) {
	store := &fakeStore{purgedRows: 3}
	effective := func() config.App {
		cfg := testEffective()
		cfg.Runner.AllowedCategories = []string{"radarr", "sonarr"}
		return cfg
	}
	srv := New(store, func() {}, effective, ":0", runner.NewState(), nil)

	body := []byte(`{"start_hour":8,"list_refresh_per_day":4,"threshold_month":1,"threshold_half_year":2,"threshold_year":3,"monitored_categories":["radarr","sonarr"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/schedule", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutSchedule(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}
	// релизы выключенной категории иначе висят в плане, а форс на них молчит
	if !slices.Equal(store.purgedCategories, []string{"radarr", "sonarr"}) {
		t.Fatalf("purge called with %v, want the monitored categories", store.purgedCategories)
	}
}

func newRegistryTestServer(store *fakeStore, reload func()) *Server {
	return withTestSession(New(store, reload, func() config.App {
		cfg := testEffective()
		cfg.Tracker = registry.ConfigDefaults()
		return cfg
	}, ":0", runner.NewState(), nil))
}

func getTrackerKeys(t *testing.T, srv *Server) []string {
	t.Helper()
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/trackers", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp []struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(resp))
	for _, it := range resp {
		keys = append(keys, it.Key)
	}
	return keys
}

func TestGetTrackersCustomOrder(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers_order": json.RawMessage(`["nnm","rutracker","bogus","nnm"]`),
	}}
	got := getTrackerKeys(t, newRegistryTestServer(store, func() {}))

	want := []string{"nnm", "rutracker"}
	for _, k := range registry.Keys() {
		if k != "nnm" && k != "rutracker" {
			want = append(want, k)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
}

func TestGetTrackersOrderInvalidJSONFallsBack(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers_order": json.RawMessage(`{"x":1}`),
	}}
	got := getTrackerKeys(t, newRegistryTestServer(store, func() {}))
	if !slices.Equal(got, registry.Keys()) {
		t.Fatalf("keys = %v, want id order %v", got, registry.Keys())
	}
}

func TestPutTrackersOrderSavesWithoutReload(t *testing.T) {
	store := &fakeStore{}
	reloads := 0
	srv := newRegistryTestServer(store, func() { reloads++ })

	req := httptest.NewRequest(http.MethodPut, "/api/trackers/order", strings.NewReader(`{"order":["kinozal","rutracker"]}`))
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}
	var saved []string
	if err := json.Unmarshal(store.settings["trackers_order"], &saved); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(saved, []string{"kinozal", "rutracker"}) {
		t.Fatalf("saved = %v", saved)
	}
	if reloads != 0 {
		t.Fatalf("reload called %d times, want 0", reloads)
	}
	if got := getTrackerKeys(t, srv); got[0] != "kinozal" || got[1] != "rutracker" {
		t.Fatalf("keys = %v, want kinozal, rutracker first", got)
	}
}

func TestPutTrackersOrderRejects(t *testing.T) {
	for name, body := range map[string]string{
		"unknown":   `{"order":["rutracker","bogus"]}`,
		"duplicate": `{"order":["rutracker","rutracker"]}`,
		"not array": `{"order":"x"}`,
		"not keys":  `{"order":[1]}`,
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{}
			reloads := 0
			srv := newRegistryTestServer(store, func() { reloads++ })
			req := httptest.NewRequest(http.MethodPut, "/api/trackers/order", strings.NewReader(body))
			w := httptest.NewRecorder()
			srv.srv.Handler.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
			}
			if _, ok := store.settings["trackers_order"]; ok {
				t.Fatal("trackers_order must stay unset")
			}
			if reloads != 0 {
				t.Fatalf("reload called %d times", reloads)
			}
		})
	}
}

func TestPostIntegrationsTestNotifyChannelOnly(t *testing.T) {
	origResolver := telegramChatIDResolver
	origSend := sendNotification
	defer func() {
		telegramChatIDResolver = origResolver
		sendNotification = origSend
	}()

	resolved := false
	telegramChatIDResolver = func(context.Context, string, string) (string, error) {
		resolved = true
		return "1", nil
	}
	var sent notify.Config
	sendNotification = func(_ context.Context, cfg notify.Config, _ string, _ *webpush.Store, _ notify.Event) error {
		sent = cfg
		return nil
	}

	srv := newTestServer(&fakeStore{})
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.handleTestIntegration(w, req)
		return w
	}

	w := post(`{"kind":"notify","config":{"channel":"webhook","telegram":{"bot_token":"tok"},"webhook":{"url":"http://hook"},"enabled":true}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if resolved {
		t.Error("chat id resolver called although telegram is not the tested channel")
	}
	if sent.TelegramToken != "" || sent.TelegramChatID != "" || sent.WebhookURL != "http://hook" {
		t.Errorf("sent config = %+v, want webhook only", sent)
	}

	w = post(`{"kind":"notify","config":{"channel":"telegram","telegram":{"bot_token":"tok"},"webhook":{"url":"http://hook"},"enabled":true}}`)
	if w.Code != http.StatusOK || !resolved {
		t.Fatalf("telegram channel: status %d resolved %v", w.Code, resolved)
	}
	if sent.WebhookURL != "" || sent.TelegramToken != "tok" {
		t.Errorf("sent config = %+v, want telegram only", sent)
	}

	if w = post(`{"kind":"notify","config":{"channel":"sms","enabled":true}}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown channel status = %d, want 400", w.Code)
	}
}

func TestPostIntegrationsTestNotifyEmailChannel(t *testing.T) {
	origSend := sendNotification
	defer func() { sendNotification = origSend }()
	var sent notify.Config
	sendNotification = func(_ context.Context, cfg notify.Config, _ string, _ *webpush.Store, _ notify.Event) error {
		sent = cfg
		return nil
	}
	srv := newTestServer(&fakeStore{})
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.handleTestIntegration(w, req)
		return w
	}
	const all = `"webhook":{"url":"http://hook"},"email":{"host":"smtp.example.com","port":465,"security":"tls","username":"u","password":"p","from":"f@x.example","to":"t@x.example"},"pushover":{"app_token":"a","user_key":"k"},"enabled":true`

	if w := post(`{"kind":"notify","config":{"channel":"email",` + all + `}}`); w.Code != http.StatusOK {
		t.Fatalf("email status = %d: %s", w.Code, w.Body.String())
	}
	if sent.EmailHost != "smtp.example.com" || sent.EmailPort != 465 || sent.EmailSecurity != "tls" || sent.EmailUsername != "u" ||
		sent.EmailPassword != "p" || sent.EmailFrom != "f@x.example" || sent.EmailTo != "t@x.example" {
		t.Errorf("email fields = %+v", sent)
	}
	if sent.WebhookURL != "" || sent.PushoverToken != "" || sent.PushoverUser != "" {
		t.Errorf("other channels not zeroed: %+v", sent)
	}

	if w := post(`{"kind":"notify","config":{"channel":"pushover",` + all + `}}`); w.Code != http.StatusOK {
		t.Fatalf("pushover status = %d: %s", w.Code, w.Body.String())
	}
	if sent.PushoverToken != "a" || sent.PushoverUser != "k" || sent.EmailHost != "" || sent.WebhookURL != "" {
		t.Errorf("pushover config = %+v", sent)
	}
}

func fakeDelugeWeb(t *testing.T, password string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
			ID     int64  `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || r.URL.Path != "/json" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var result any
		switch req.Method {
		case "auth.login":
			result = len(req.Params) == 1 && req.Params[0] == password
		case "web.connected":
			result = true
		default:
			t.Errorf("unexpected deluge method %q", req.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": nil, "id": req.ID})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleTestIntegrationDeluge(t *testing.T) {
	dl := fakeDelugeWeb(t, "dlpass")
	effective := func() config.App {
		cfg := testEffective()
		cfg.Manager.Client = "qbit"
		cfg.Manager.QbitTorrent.Host = "http://127.0.0.1:1"
		cfg.Manager.QbitTorrent.Username = "u"
		cfg.Manager.QbitTorrent.Password = "p"
		cfg.Manager.Deluge.Host = dl.URL
		cfg.Manager.Deluge.Password = "dlpass"
		return cfg
	}
	srv := New(&fakeStore{}, func() {}, effective, ":0", runner.NewState(), nil)
	run := func(body string) testResultResp {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader([]byte(body)))
		w := httptest.NewRecorder()
		srv.handleTestIntegration(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("body %s: status = %d, want 200: %s", body, w.Code, w.Body.String())
		}
		var resp testResultResp
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// kind=deluge берет данные deluge, даже когда активен qbit
	if resp := run(`{"kind":"deluge"}`); !resp.OK {
		t.Fatalf("saved deluge config: error = %q, want ok", resp.Error)
	}
	hostOnly := strings.TrimPrefix(dl.URL, "http://")
	if resp := run(`{"kind":"deluge","config":{"host":"` + hostOnly + `","password":"dlpass"}}`); !resp.OK {
		t.Fatalf("form config without scheme: error = %q, want ok", resp.Error)
	}
	if resp := run(`{"kind":"deluge","config":{"host":"` + dl.URL + `","password":"wrong"}}`); resp.OK || !strings.Contains(resp.Error, "login failed") {
		t.Fatalf("wrong password: resp = %+v, want login failed", resp)
	}
}

func TestRedownloadDelugeReturns501(t *testing.T) {
	redownloadSlot <- struct{}{}
	defer func() { <-redownloadSlot }()

	effective := func() config.App {
		cfg := testEffective()
		cfg.Manager.Client = " Deluge "
		return cfg
	}
	srv := New(&fakeStore{}, func() {}, effective, ":0", runner.NewState(), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/history/somehash/redownload", nil)
	req.SetPathValue("hash", "somehash")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.handleRedownload(w, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleRedownload blocked instead of returning 501 for deluge")
	}
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501, body: %s", w.Code, w.Body.String())
	}
	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp["error"] != deluge.ErrRedownloadUnsupported.Error() {
		t.Fatalf("error = %q, want %q", errResp["error"], deluge.ErrRedownloadUnsupported.Error())
	}
}

func TestGetCategoriesUsesDelugeConfig(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()
	var gotKind string
	var gotCfg model.ManagerConfig
	managerClientFactory = func(kind string, cfg model.ManagerConfig) (managerClient, error) {
		gotKind, gotCfg = kind, cfg
		return &fakeQbitClient{cats: []string{"tv"}}, nil
	}
	effective := func() config.App {
		cfg := testEffective()
		cfg.Manager.Client = "deluge"
		cfg.Manager.QbitTorrent.Host = "http://qb:8068"
		cfg.Manager.Deluge.Host = "http://dl:8112"
		cfg.Manager.Deluge.Password = "p"
		return cfg
	}
	srv := New(&fakeStore{}, func() {}, effective, ":0", runner.NewState(), nil)
	w := httptest.NewRecorder()
	srv.handleGetCategories(w, httptest.NewRequest(http.MethodGet, "/api/categories", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if gotKind != "deluge" || gotCfg.Host != "http://dl:8112" || gotCfg.Password != "p" || gotCfg.Username != "" {
		t.Fatalf("factory got kind=%q cfg=%+v, want deluge creds", gotKind, gotCfg)
	}
}

func TestSecurityHeaders(t *testing.T) {
	cases := []struct {
		name string
		srv  *Server
		path string
	}{
		{"index", newTestServer(&fakeStore{}), "/"},
		{"login", newTestServer(&fakeStore{}), "/login"},
		{"auth state", newTestServer(&fakeStore{}), "/api/auth/state"},
		{"healthz", newTestServer(&fakeStore{}), "/healthz"},
		{"unauthorized", newBareServer(&fakeStore{}), "/api/history"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c.srv.srv.Handler.ServeHTTP(rec, httptest.NewRequest("GET", c.path, nil))
			if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Fatalf("X-Frame-Options = %q, want DENY (status %d)", got, rec.Code)
			}
			if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
				t.Fatalf("Content-Security-Policy = %q, want frame-ancestors 'none'", got)
			}
		})
	}
}

func TestPutIntegrationsNotifyLanguage(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)
	put := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		srv.handlePutIntegrations(w, httptest.NewRequest(http.MethodPut, "/api/integrations", strings.NewReader(body)))
		return w
	}
	if w := put(`{"notifications":{"language":"ru"}}`); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}
	var saved struct {
		Notifications struct {
			Language string `json:"language"`
		} `json:"notifications"`
	}
	if err := json.Unmarshal(store.settings["integrations"], &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Notifications.Language != "ru" {
		t.Fatalf("stored language = %q, want ru", saved.Notifications.Language)
	}
	before := string(store.settings["integrations"])
	w := put(`{"notifications":{"language":"xx"}}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "notifications.language must be en or ru") {
		t.Fatalf("status = %d body %s, want 400 with language error", w.Code, w.Body.String())
	}
	if string(store.settings["integrations"]) != before {
		t.Fatalf("store changed on rejected patch: %s", store.settings["integrations"])
	}
}

func TestPostIntegrationsTestNotifyCarriesLanguage(t *testing.T) {
	origSend := sendNotification
	defer func() { sendNotification = origSend }()
	var sentCfg notify.Config
	var sentEv notify.Event
	sendNotification = func(_ context.Context, cfg notify.Config, _ string, _ *webpush.Store, ev notify.Event) error {
		sentCfg, sentEv = cfg, ev
		return nil
	}
	effective := func() config.App {
		cfg := testEffective()
		cfg.Notify.WebhookURL = "http://hook.invalid"
		cfg.Notify.Language = "ru"
		cfg.Web.AppURL = "https://rip.example.com"
		return cfg
	}
	srv := New(&fakeStore{}, func() {}, effective, ":0", runner.NewState(), nil)
	for _, c := range []struct{ body, want string }{
		{`{"kind":"notify","config":{"webhook":{"url":"http://hook.invalid"},"channel":"webhook","language":"en"}}`, "en"},
		{`{"kind":"notify","config":{"webhook":{"url":"http://hook.invalid"},"channel":"webhook"}}`, "ru"},
	} {
		w := httptest.NewRecorder()
		srv.handleTestIntegration(w, httptest.NewRequest(http.MethodPost, "/api/integrations/test", strings.NewReader(c.body)))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body: %s", c.body, w.Code, w.Body.String())
		}
		if sentCfg.Language != c.want {
			t.Errorf("%s: cfg.Language = %q, want %q", c.body, sentCfg.Language, c.want)
		}
		if sentEv.Kind != notify.KindTest || sentEv.AppLink != "https://rip.example.com/#dashboard" {
			t.Errorf("%s: event = %+v, want KindTest with app link", c.body, sentEv)
		}
		if want := notify.TestEvent(sentEv.AppLink); sentEv.Text != want.Text {
			t.Errorf("%s: Text = %q, want %q", c.body, sentEv.Text, want.Text)
		}
	}
}

func TestPutScheduleTagsModeSkipsCategoryPurge(t *testing.T) {
	store := &fakeStore{purgedRows: 3}
	effective := func() config.App {
		cfg := testEffective()
		cfg.Runner.AllowedCategories = []string{"tv"}
		cfg.Runner.WatchMode = config.WatchModeTags
		return cfg
	}
	srv := New(store, func() {}, effective, ":0", runner.NewState(), nil)

	body := []byte(`{"start_hour":8,"list_refresh_per_day":4,"threshold_month":1,"threshold_half_year":2,"threshold_year":3,"watch_mode":"tags"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/schedule", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.handlePutSchedule(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}
	// в режиме тегов список категорий не используется, чистка по нему удалила бы нужные релизы
	if store.purgedCategories != nil {
		t.Fatalf("purge called with %v in tags mode", store.purgedCategories)
	}
}

func TestPutScheduleMirrorConfirmOnly(t *testing.T) {
	stored := `{"start_hour":9,"list_refresh_per_day":4,"threshold_month":1,"threshold_half_year":2,"threshold_year":3,"mirror_confirm":false,"watch_tags":["a"],"timezone":"UTC"}`
	for name, initial := range map[string]string{"full": stored, "empty": ""} {
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{}
			if initial != "" {
				store.settings = map[string]json.RawMessage{"schedule": json.RawMessage(initial)}
			}
			srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)
			req := httptest.NewRequest(http.MethodPut, "/api/schedule", strings.NewReader(`{"mirror_confirm":true}`))
			w := httptest.NewRecorder()
			srv.handlePutSchedule(w, req)
			if w.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
			}
			var got map[string]any
			if err := json.Unmarshal(store.settings["schedule"], &got); err != nil {
				t.Fatal(err)
			}
			if got["mirror_confirm"] != true {
				t.Errorf("mirror_confirm = %v, want true", got["mirror_confirm"])
			}
			if initial != "" && (got["start_hour"] != float64(9) || got["timezone"] != "UTC" || len(got["watch_tags"].([]any)) != 1) {
				t.Errorf("stored fields changed: %v", got)
			}
		})
	}
}

func TestPostHistoryPoll(t *testing.T) {
	post := func(srv *Server, header bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/history/poll", nil)
		if header {
			req.Header.Set("X-Ripperr", "1")
		}
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		return w
	}
	poller := func(state *runner.State, calls *int, n int, err error) {
		state.SetPoller(state.Reset(), func(context.Context) (int, error) {
			*calls++
			return n, err
		})
	}

	t.Run("no poller", func(t *testing.T) {
		srv := newTestServer(&fakeStore{})
		if w := post(srv, true); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
	})
	t.Run("waiting settings", func(t *testing.T) {
		state := runner.NewState()
		calls := 0
		poller(state, &calls, 2, nil)
		state.SetWaitingSettings(true)
		srv := withTestSession(New(&fakeStore{}, func() {}, testEffective, ":0", state, nil))
		if w := post(srv, true); w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", w.Code)
		}
		if calls != 0 {
			t.Fatalf("poller called %d times while settings are unreadable", calls)
		}
	})
	t.Run("changed", func(t *testing.T) {
		state := runner.NewState()
		calls := 0
		poller(state, &calls, 2, nil)
		srv := withTestSession(New(&fakeStore{}, func() {}, testEffective, ":0", state, nil))
		w := post(srv, true)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
		}
		var resp map[string]int
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if resp["changed"] != 2 || calls != 1 {
			t.Fatalf("resp = %v, calls = %d, want changed 2 and 1 call", resp, calls)
		}
	})
	t.Run("poller error", func(t *testing.T) {
		state := runner.NewState()
		calls := 0
		poller(state, &calls, 0, errors.New("qbit down"))
		srv := withTestSession(New(&fakeStore{}, func() {}, testEffective, ":0", state, nil))
		if w := post(srv, true); w.Code != http.StatusBadGateway {
			t.Fatalf("status = %d, want 502", w.Code)
		}
	})
	t.Run("csrf and GET", func(t *testing.T) {
		state := runner.NewState()
		calls := 0
		poller(state, &calls, 1, nil)
		srv := New(&fakeStore{}, func() {}, testEffective, ":0", state, nil)
		if w := post(srv, false); w.Code != http.StatusForbidden {
			t.Fatalf("status without X-Ripperr = %d, want 403", w.Code)
		}
		w := httptest.NewRecorder()
		srv = withTestSession(srv)
		srv.srv.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/history/poll", nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("GET status = %d, want 404 (catch-all route, no poll)", w.Code)
		}
		if calls != 0 {
			t.Fatalf("poller called %d times by rejected requests", calls)
		}
	})
}

func TestPostPlanForceNewRelease(t *testing.T) {
	store := &fakeStore{forceItems: map[string]bool{}, newReleases: map[string]bool{"n1": true}}
	state := runner.NewState()
	srv := New(store, func() {}, testEffective, ":0", state, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/plan/n1/force", bytes.NewReader([]byte(`{"force":true}`)))
	req.SetPathValue("hash", "n1")
	w := httptest.NewRecorder()
	srv.handlePlanForce(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var resp map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["force"] || !resp["force_queued"] {
		t.Fatalf("resp = %v, want force false and force_queued true", resp)
	}
	select {
	case h := <-state.Force():
		if h != "n1" {
			t.Fatalf("queued %q, want n1", h)
		}
	default:
		t.Fatal("expected force request queued")
	}
	if len(store.forceItems) != 0 {
		t.Fatalf("forceItems touched: %v", store.forceItems)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/plan/n1/force", bytes.NewReader([]byte(`{"force":false}`)))
	req.SetPathValue("hash", "n1")
	w = httptest.NewRecorder()
	srv.handlePlanForce(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("clearing force on a new row: status = %d, want 404", w.Code)
	}
}

func TestGetHistoryNewReleaseTimes(t *testing.T) {
	first := time.Date(2026, 10, 26, 7, 0, 0, 0, time.UTC)
	store := &fakeStore{checkLog: []storage.CheckLogEntry{{ID: 1, URLHash: "n1", ItemName: "New", Status: "new", RecheckAt: &first}}}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, httptest.NewRequest(http.MethodGet, "/api/history?status=new", nil))
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items = %d, want 1 (status=new passes through)", len(resp.Items))
	}
	if got := resp.Items[0]["checked_at"]; got != "" {
		t.Fatalf("checked_at = %v, want empty", got)
	}
	if got := resp.Items[0]["recheck_at"]; got != first.Format(time.RFC3339) {
		t.Fatalf("recheck_at = %v, want %s", got, first.Format(time.RFC3339))
	}
}

func TestPostPlanForceOffCancelsQueued(t *testing.T) {
	store := &fakeStore{forceItems: map[string]bool{"abc123": true}}
	srv := newTestServer(store)
	gen := srv.runnerState.Reset()
	srv.runnerState.Begin(gen)
	srv.runnerState.Enqueue(gen, "abc123", true)

	req := httptest.NewRequest(http.MethodPost, "/api/plan/abc123/force", bytes.NewReader([]byte(`{"force":false}`)))
	req.SetPathValue("hash", "abc123")
	w := httptest.NewRecorder()
	srv.handlePlanForce(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if _, total := srv.runnerState.QueuedItems(10); total != 0 {
		t.Fatalf("queued total = %d after force off, want 0", total)
	}
	if !srv.runnerState.ForceCancelled("abc123") {
		t.Fatal("expected the forced task to be marked cancelled")
	}
}

func TestPostPlanForceAllClearCancelsQueued(t *testing.T) {
	store := &fakeStore{forceItems: map[string]bool{"h1": true, "h2": true, "h3": true}}
	srv := newTestServer(store)
	gen := srv.runnerState.Reset()
	srv.runnerState.Begin(gen)
	for _, h := range []string{"h1", "h2", "h3"} {
		srv.runnerState.Enqueue(gen, h, true)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/plan/force-all", bytes.NewReader([]byte(`{"force":false}`)))
	w := httptest.NewRecorder()
	srv.handlePlanForceAll(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if _, total := srv.runnerState.QueuedItems(10); total != 0 {
		t.Fatalf("queued total = %d after force-all clear, want 0", total)
	}
	for _, h := range []string{"h1", "h2", "h3"} {
		if !srv.runnerState.ForceCancelled(h) {
			t.Fatalf("%s not marked cancelled", h)
		}
	}
}

func fakeDownloadStationWeb(t *testing.T, password string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		reply := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		switch r.URL.Path {
		case "/webapi/query.cgi":
			info := map[string]any{}
			for _, name := range strings.Split(r.Form.Get("query"), ",") {
				info[name] = map[string]any{"path": "entry.cgi", "minVersion": 1, "maxVersion": 7}
			}
			info["SYNO.API.Auth"] = map[string]any{"path": "auth.cgi", "minVersion": 1, "maxVersion": 7}
			reply(map[string]any{"success": true, "data": info})
		case "/webapi/auth.cgi":
			if r.Form.Get("passwd") != password {
				reply(map[string]any{"success": false, "error": map[string]any{"code": 400}})
				return
			}
			reply(map[string]any{"success": true, "data": map[string]any{"sid": "sid1"}})
		default:
			t.Errorf("unexpected downloadstation path %q", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleTestIntegrationDownloadStation(t *testing.T) {
	ds := fakeDownloadStationWeb(t, "dspass")
	effective := func() config.App {
		cfg := testEffective()
		cfg.Manager.Client = "qbit"
		cfg.Manager.QbitTorrent.Host = "http://127.0.0.1:1"
		cfg.Manager.DownloadStation.Host = ds.URL
		cfg.Manager.DownloadStation.Username = "u"
		cfg.Manager.DownloadStation.Password = "dspass"
		return cfg
	}
	srv := New(&fakeStore{}, func() {}, effective, ":0", runner.NewState(), nil)
	run := func(body string) testResultResp {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader([]byte(body)))
		w := httptest.NewRecorder()
		srv.handleTestIntegration(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("body %s: status = %d, want 200: %s", body, w.Code, w.Body.String())
		}
		var resp testResultResp
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if resp := run(`{"kind":"downloadstation"}`); !resp.OK {
		t.Fatalf("saved downloadstation config: error = %q, want ok", resp.Error)
	}
	hostOnly := strings.TrimPrefix(ds.URL, "http://")
	if resp := run(`{"kind":"downloadstation","config":{"host":"` + hostOnly + `","username":"u","password":"dspass"}}`); !resp.OK {
		t.Fatalf("form config without scheme: error = %q, want ok", resp.Error)
	}
	if resp := run(`{"kind":"downloadstation","config":{"host":"` + ds.URL + `","username":"u","password":"wrong"}}`); resp.OK || !strings.Contains(resp.Error, "account or password") {
		t.Fatalf("wrong password: resp = %+v, want account or password", resp)
	}
}

func TestRedownloadDownloadStationReturns501(t *testing.T) {
	redownloadSlot <- struct{}{}
	defer func() { <-redownloadSlot }()

	effective := func() config.App {
		cfg := testEffective()
		cfg.Manager.Client = " DownloadStation "
		return cfg
	}
	srv := New(&fakeStore{}, func() {}, effective, ":0", runner.NewState(), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/history/somehash/redownload", nil)
	req.SetPathValue("hash", "somehash")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.handleRedownload(w, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleRedownload blocked instead of returning 501 for downloadstation")
	}
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501, body: %s", w.Code, w.Body.String())
	}
	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp["error"] != downloadstation.ErrRedownloadUnsupported.Error() {
		t.Fatalf("error = %q, want %q", errResp["error"], downloadstation.ErrRedownloadUnsupported.Error())
	}
}

func TestTrackerTestTimeoutTLS(t *testing.T) {
	cfg := testEffective()
	cfg.Flaresolverr.MaxTimeout = 90000
	for pt, want := range map[int]time.Duration{0: 30 * time.Second, 1: 90 * time.Second, 2: 90 * time.Second} {
		if got := trackerTestTimeout(cfg, pt); got != want {
			t.Errorf("proxy_type %d: timeout = %v, want %v", pt, got, want)
		}
	}
	cfg.Flaresolverr.MaxTimeout = 0
	if got := trackerTestTimeout(cfg, 2); got != 30*time.Second {
		t.Errorf("zero max timeout: %v", got)
	}
}
