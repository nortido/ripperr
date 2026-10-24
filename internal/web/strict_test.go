package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/model"
	"ripperr/internal/runner"
	storagePkg "ripperr/internal/storage"
)

type saveCountStore struct {
	*fakeStore
	saves int
}

func (s *saveCountStore) SaveSetting(ctx context.Context, key string, value json.RawMessage) error {
	s.saves++
	return s.fakeStore.SaveSetting(ctx, key, value)
}

func failingStrict() (config.App, error) {
	return config.App{}, errors.New("db busy")
}

func TestStrictRefusesTrackerSave(t *testing.T) {
	store := &saveCountStore{fakeStore: newMaskingTrackerStore()}
	srv := newMaskingServer(store.fakeStore)
	srv.db = store
	form := maskedTrackerForm(t, srv, "rutracker")
	body, _ := json.Marshal(form)
	srv.SetStrictEffective(failingStrict)

	req := httptest.NewRequest(http.MethodPut, "/api/trackers/rutracker", bytes.NewReader(body))
	req.SetPathValue("key", "rutracker")
	w := httptest.NewRecorder()
	srv.handlePutTracker(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("PUT status = %d, want 503, body: %s", w.Code, w.Body.String())
	}

	if store.saves != 0 {
		t.Errorf("SaveSetting called %d times, want 0", store.saves)
	}
}

func TestStrictRefusesTrackerAdd(t *testing.T) {
	store := &saveCountStore{fakeStore: &fakeStore{settings: map[string]json.RawMessage{
		"trackers_added": json.RawMessage(`["rutracker"]`),
	}}}
	reloads := 0
	srv := newAddedServer(store.fakeStore, &reloads)
	srv.db = store
	srv.SetStrictEffective(failingStrict)

	w := serveAdded(srv, http.MethodPost, "/api/trackers/toloka", `{"login":"u","password":"p","domain":"toloka.to"}`)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body: %s", w.Code, w.Body.String())
	}
	if store.saves != 0 || reloads != 0 {
		t.Errorf("saves = %d reloads = %d, want 0 and 0", store.saves, reloads)
	}
}

func TestStrictRefusesIntegrationsSave(t *testing.T) {
	store := &saveCountStore{fakeStore: &fakeStore{settings: map[string]json.RawMessage{
		"integrations": json.RawMessage(maskingIntegrationsSeed),
	}}}
	srv := newMaskingServer(store.fakeStore)
	srv.db = store
	srv.SetStrictEffective(failingStrict)

	body := []byte(`{"notifications":{"telegram":{"bot_token":"********"}}}`)
	w := httptest.NewRecorder()
	srv.handlePutIntegrations(w, httptest.NewRequest(http.MethodPut, "/api/integrations", bytes.NewReader(body)))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body: %s", w.Code, w.Body.String())
	}
	if store.saves != 0 {
		t.Errorf("SaveSetting called %d times, want 0", store.saves)
	}
}

func TestStrictSchedulePurgeSkipped(t *testing.T) {
	store := &fakeStore{purgedRows: 3}
	reloaded := false
	effective := func() config.App {
		cfg := testEffective()
		cfg.Runner.AllowedCategories = []string{"tv"}
		return cfg
	}
	srv := New(store, func() { reloaded = true }, effective, ":0", runner.NewState(), nil)
	srv.SetStrictEffective(failingStrict)

	body := []byte(`{"start_hour":8,"list_refresh_per_day":4,"threshold_month":1,"threshold_half_year":2,"threshold_year":3}`)
	w := httptest.NewRecorder()
	srv.handlePutSchedule(w, httptest.NewRequest(http.MethodPut, "/api/schedule", bytes.NewReader(body)))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}
	if store.settings["schedule"] == nil {
		t.Error("schedule not saved")
	}
	if store.purgedCategories != nil {
		t.Errorf("PurgeCategoriesExcept called with %v, want no call", store.purgedCategories)
	}
	if !reloaded {
		t.Error("reload not called")
	}
}

func TestStrictRefusesRedownload(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()
	calls := 0
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		calls++
		return &commentRecordingClient{}, nil
	}
	srv := New(&fakeStore{}, func() {}, testEffective, ":0", runner.NewState(), nil)
	srv.SetStrictEffective(failingStrict)

	req := httptest.NewRequest(http.MethodPost, "/api/history/abc/redownload", nil)
	req.SetPathValue("hash", "abc")
	w := httptest.NewRecorder()
	srv.handleRedownload(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body: %s", w.Code, w.Body.String())
	}
	if calls != 0 {
		t.Errorf("client factory called %d times, want 0", calls)
	}
}

func TestStrictOkBehavesAsEffective(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()
	const topic = "https://example.com/topic/1"
	fc := &commentRecordingClient{entries: []*model.Entry{{URL: topic, Hash: "abc", Name: "x"}}}
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) { return fc, nil }
	srv := New(&fakeStore{}, func() {}, testEffective, ":0", runner.NewState(), nil)
	srv.SetStrictEffective(func() (config.App, error) { return testEffective(), nil })

	urlHash := storagePkg.URLHash(topic)
	req := httptest.NewRequest(http.MethodPost, "/api/history/"+urlHash+"/redownload", nil)
	req.SetPathValue("hash", urlHash)
	w := httptest.NewRecorder()
	srv.handleRedownload(w, req)
	if w.Code != http.StatusOK || fc.calls != 1 {
		t.Fatalf("status = %d calls = %d, want 200 and 1, body: %s", w.Code, fc.calls, w.Body.String())
	}
}

func TestRunRefusedWhileWaitingForSettings(t *testing.T) {
	state := runner.NewState()
	srv := New(&fakeStore{}, func() {}, testEffective, ":0", state, nil)
	state.SetWaitingSettings(true)

	for _, body := range []string{"", `{"dry":true}`} {
		req := httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader([]byte(body)))
		w := httptest.NewRecorder()
		srv.handleRun(w, req)
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("body %q: status = %d, want 503", body, w.Code)
		}
	}
	select {
	case <-state.Trigger():
		t.Fatal("trigger queued while waiting for settings")
	default:
	}

	state.SetWaitingSettings(false)
	w := httptest.NewRecorder()
	srv.handleRun(w, httptest.NewRequest(http.MethodPost, "/api/run", nil))
	if w.Code != http.StatusAccepted {
		t.Fatalf("after wait: status = %d, want 202", w.Code)
	}
}

func TestRunWaitingBeatsRunningPass(t *testing.T) {
	state := runner.NewState()
	srv := New(&fakeStore{}, func() {}, testEffective, ":0", state, nil)
	state.Begin(state.Reset())
	state.SetWaitingSettings(true)

	for _, body := range []string{"", `{"restart":true}`} {
		w := httptest.NewRecorder()
		srv.handleRun(w, httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader([]byte(body))))
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("body %q: status = %d, want 503", body, w.Code)
		}
	}
	if state.Stopping() {
		t.Fatal("stopping set before the stop request")
	}

	w := httptest.NewRecorder()
	srv.handleRun(w, httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader([]byte(`{"stop":true}`))))
	if w.Code != http.StatusAccepted || !state.Stopping() {
		t.Errorf("stop: status = %d stopping = %v, want 202 and true", w.Code, state.Stopping())
	}
}
