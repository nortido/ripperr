package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"trackerr/internal/config"
	"trackerr/internal/helper/breakeralerts"
	"trackerr/internal/model"
	"trackerr/internal/model/storage"
	"trackerr/internal/model/tracker"
	"trackerr/internal/runner"
	"trackerr/internal/settings"
	storagePkg "trackerr/internal/storage"
	"trackerr/internal/system/logger"
)

type fakeStore struct {
	checkLog   []storage.CheckLogEntry
	ignore     []storage.IgnoreEntry
	settings   map[string]json.RawMessage
	forceItems map[string]bool
	pingErr    error
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

func (f *fakeStore) ListCheckLog(_ context.Context, limit, offset int, statusFilter string, ignoredOnly bool, sortBy string, nameFilter string, brokenStreak int) ([]storage.CheckLogEntry, int, error) {
	ignoreSet := make(map[string]struct{}, len(f.ignore))
	for _, e := range f.ignore {
		ignoreSet[e.URLHash] = struct{}{}
	}
	var filtered []storage.CheckLogEntry
	nameNeedle := strings.ToLower(nameFilter)
	for _, e := range dedupeCheckLogByName(f.checkLog) {
		if statusFilter == "broken" {
			if brokenStreak < 1 {
				brokenStreak = 1
			}
			if e.ErrorStreak < brokenStreak {
				continue
			}
		} else if statusFilter != "" && e.Status != statusFilter {
			continue
		}
		if nameFilter != "" && !strings.Contains(strings.ToLower(e.ItemName), nameNeedle) {
			continue
		}
		_, inIgnore := ignoreSet[e.URLHash]
		if ignoredOnly && !inIgnore {
			continue
		}
		filtered = append(filtered, e)
	}
	if sortBy == "recheck" {
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
	if offset > len(filtered) {
		return nil, total, nil
	}
	filtered = filtered[offset:]
	if limit > 0 && len(filtered) > limit {
		filtered = filtered[:limit]
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

func (f *fakeStore) ForceCheckAll(_ context.Context, force bool) (int64, error) {
	if f.forceItems == nil {
		return 0, nil
	}
	ignoreSet, _ := f.GetIgnoreSet(context.Background())
	var n int64
	for hash := range f.forceItems {
		if force {
			if _, ignored := ignoreSet[hash]; ignored {
				continue
			}
			f.forceItems[hash] = true
			n++
		} else {
			f.forceItems[hash] = false
			n++
		}
	}
	return n, nil
}

func (f *fakeStore) GetTracker(context.Context, int) (*tracker.Tracker, error) {
	return &tracker.Tracker{}, nil
}

func (f *fakeStore) Ping(context.Context) error {
	return f.pingErr
}

func newTestServer(store *fakeStore) *Server {
	return New(store, func() {}, testEffective, ":0", runner.NewState(), nil)
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

func (f *fakeQbitClient) ForceRedownload(ctx context.Context, _ string) error {
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

	req := httptest.NewRequest(http.MethodGet, "/style.css", nil)
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

	req2 := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", w2.Code)
	}
}

func TestGetCategories(t *testing.T) {
	fakeQbitFactory := func(cats []string, err error) func(model.ManagerConfig) (qbitCategoriesClient, error) {
		return func(_ model.ManagerConfig) (qbitCategoriesClient, error) {
			if err != nil {
				return nil, err
			}
			return &fakeQbitClient{cats: cats}, nil
		}
	}

	orig := qbitClientFactory
	defer func() { qbitClientFactory = orig }()

	store := &fakeStore{}
	srv := newTestServer(store)

	qbitClientFactory = fakeQbitFactory([]string{"tv", "movies"}, nil)
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

	qbitClientFactory = fakeQbitFactory(nil, fmt.Errorf("connection refused"))
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

func TestGetTimezones(t *testing.T) {
	store := &fakeStore{}
	srv := newTestServer(store)
	req := httptest.NewRequest(http.MethodGet, "/api/timezones", nil)
	w := httptest.NewRecorder()
	srv.handleGetTimezones(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Timezones []struct {
			ID     string `json:"id"`
			Offset string `json:"offset"`
		} `json:"timezones"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Timezones) < 40 {
		t.Fatalf("expected >=40 zones, got %d", len(resp.Timezones))
	}
	if resp.Timezones[0].ID != "UTC" || resp.Timezones[0].Offset != "+00:00" {
		t.Fatalf("first zone = %+v, want UTC +00:00", resp.Timezones[0])
	}
	for _, tz := range resp.Timezones {
		if tz.ID == "Europe/Moscow" && tz.Offset != "+03:00" {
			t.Fatalf("Europe/Moscow offset = %q, want +03:00", tz.Offset)
		}
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
	if resp.Updated != 2 {
		t.Fatalf("updated = %d, want 2", resp.Updated)
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
	cfg.Tracker.Kinozal.ProxyType = 0
	patch := json.RawMessage(`{"proxy_type":1}`)
	if got := effectiveTrackerProxyType(cfg, "kinozal", patch); got != 1 {
		t.Fatalf("proxy_type = %d, want 1", got)
	}
}

type capturingStore struct {
	fakeStore
	lastLimit int
}

func (c *capturingStore) ListCheckLog(ctx context.Context, limit, offset int, statusFilter string, ignoredOnly bool, sortBy string, nameFilter string, brokenStreak int) ([]storage.CheckLogEntry, int, error) {
	c.lastLimit = limit
	return c.fakeStore.ListCheckLog(ctx, limit, offset, statusFilter, ignoredOnly, sortBy, nameFilter, brokenStreak)
}

func TestGetHistoryLimitCap(t *testing.T) {
	store := &capturingStore{fakeStore: fakeStore{checkLog: []storage.CheckLogEntry{{ID: 1}}}}
	srv := New(store, func() {}, testEffective, ":0", runner.NewState(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/history?limit=100000", nil)
	w := httptest.NewRecorder()
	srv.handleHistory(w, req)
	if store.lastLimit != 1000 {
		t.Fatalf("limit = %d, want 1000", store.lastLimit)
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

func TestHealthzDatabaseError(t *testing.T) {
	store := &fakeStore{pingErr: fmt.Errorf("connection failed")}
	srv := newTestServer(store)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.handleHealthz(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "error" {
		t.Fatalf("status = %q, want error", resp["status"])
	}
	if resp["error"] != "connection failed" {
		t.Fatalf("error = %q, want connection failed", resp["error"])
	}
}

func TestHandleRedownload(t *testing.T) {
	fakeQbitFactory := func(entries []*model.Entry, forceRedownloadErr error) func(model.ManagerConfig) (qbitCategoriesClient, error) {
		return func(_ model.ManagerConfig) (qbitCategoriesClient, error) {
			return &fakeQbitClient{
				entries:            entries,
				forceRedownloadErr: forceRedownloadErr,
			}, nil
		}
	}

	orig := qbitClientFactory
	defer func() { qbitClientFactory = orig }()

	store := &fakeStore{}
	srv := newTestServer(store)

	// Test success
	testURL := "https://rutracker.org/forum/viewtopic.php?t=123"
	testURLHash := storagePkg.URLHash(testURL)
	entries := []*model.Entry{
		{
			URL:  testURL,
			Hash: "abc123",
			Name: "Test Torrent",
		},
	}

	qbitClientFactory = fakeQbitFactory(entries, nil)
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

	// Test torrent not found
	qbitClientFactory = fakeQbitFactory(nil, nil)
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
	if errResp["error"] != "torrent not found in qbittorrent" {
		t.Fatalf("error = %q, want torrent not found in qbittorrent", errResp["error"])
	}

	// Test ForceRedownload error
	entries = []*model.Entry{
		{
			URL:  testURL,
			Hash: "abc123",
			Name: "Test Torrent",
		},
	}
	qbitClientFactory = fakeQbitFactory(entries, fmt.Errorf("redownload failed"))
	req = httptest.NewRequest(http.MethodPost, "/api/history/"+testURLHash+"/redownload", nil)
	req.SetPathValue("hash", testURLHash)
	w = httptest.NewRecorder()
	srv.handleRedownload(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502, body: %s", w.Code, w.Body.String())
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
	orig := qbitClientFactory
	defer func() { qbitClientFactory = orig }()

	testURL := "https://rutracker.org/forum/viewtopic.php?t=777"
	testURLHash := storagePkg.URLHash(testURL)
	entries := []*model.Entry{{URL: testURL, Hash: "abc777", Name: "Serialized"}}
	tracker := &concurrencyTracker{}

	qbitClientFactory = func(_ model.ManagerConfig) (qbitCategoriesClient, error) {
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
