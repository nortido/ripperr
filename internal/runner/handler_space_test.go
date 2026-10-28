package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/model"
	"ripperr/internal/notify"
)

type scriptedSubmitManager struct {
	fakeManager
	errs []error
	n    int
}

func (m *scriptedSubmitManager) Submit(context.Context, *bytes.Buffer, model.ManagerAddFileOptions) error {
	err := m.errs[m.n]
	m.n++
	return err
}

func noSpaceErr(hash string) error {
	return fmt.Errorf("submit error: %w", &model.NoSpaceError{Path: "/d", Need: 9 << 30, Free: 1 << 30, Hash: hash})
}

func newNoSpaceHandler(t *testing.T, mgr *scriptedSubmitManager) (*handler, *loggingStorage, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var kinds []string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var body struct {
			Event string `json:"event"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		kinds = append(kinds, body.Event)
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)
	store := &loggingStorage{fakeStorage: fakeStorage{checkStreak: 5}}
	h := &handler{
		config:  model.HandlerConfig{ErrorStreakThreshold: 3, ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage: store,
		manager: mgr,
		trackerService: &updatingPageTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: bytes.NewBufferString(strings.Repeat("x", 60))},
		},
		notifier: notify.New(notify.Config{WebhookURL: srv.URL, Events: []string{notify.KindNoSpace, notify.KindCheckFailed}, Enabled: true}, ""),
		mx:       helper.KeyedMutex{},
	}
	seen := func() []string {
		time.Sleep(300 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), kinds...)
	}
	return h, store, seen
}

func noSpaceEntry() *model.Entry {
	old := time.Now().Add(-48 * time.Hour)
	return &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc", Hash: "old1", Timestamp: &old}
}

func TestWorkerDefersOnNoSpace(t *testing.T) {
	mgr := &scriptedSubmitManager{errs: []error{noSpaceErr("n1"), noSpaceErr("n1"), noSpaceErr("n1"), noSpaceErr("n1"), noSpaceErr("n2")}}
	h, store, seen := newNoSpaceHandler(t, mgr)
	e := noSpaceEntry()
	for i := 0; i < 4; i++ {
		_ = h.worker(context.Background(), 1, e, nil)
	}
	if len(store.logs) != 4 {
		t.Fatalf("logs = %d, want 4", len(store.logs))
	}
	for _, l := range store.logs {
		if l.Status != "error" || !l.KeepStreak || !strings.Contains(l.Error, "not enough free disk space") {
			t.Fatalf("log = %+v, want error with KeepStreak and no-space text", l)
		}
	}
	if len(store.ignoreCalls) != 0 || len(store.saved) != 0 {
		t.Fatalf("ignoreCalls = %v, saved = %d, want none", store.ignoreCalls, len(store.saved))
	}
	if k := seen(); len(k) != 1 || k[0] != notify.KindNoSpace {
		t.Fatalf("events = %v, want [no_space]", k)
	}
	_ = h.worker(context.Background(), 1, e, nil)
	if k := seen(); len(k) != 2 || k[1] != notify.KindNoSpace {
		t.Fatalf("events = %v, want two no_space", k)
	}
}

func TestWorkerNoSpaceNotifiesAgainAfterSuccess(t *testing.T) {
	mgr := &scriptedSubmitManager{errs: []error{noSpaceErr("n1"), nil, noSpaceErr("n1")}}
	h, _, seen := newNoSpaceHandler(t, mgr)
	e := noSpaceEntry()
	for i := 0; i < 3; i++ {
		_ = h.worker(context.Background(), 1, e, nil)
	}
	k := seen()
	if len(k) != 2 || k[0] != notify.KindNoSpace || k[1] != notify.KindNoSpace {
		t.Fatalf("events = %v, want two no_space", k)
	}
}

func TestWorkerDefersOnPartialList(t *testing.T) {
	perr := fmt.Errorf("x: %w", model.ErrPartialList)
	mgr := &scriptedSubmitManager{errs: []error{perr, perr, perr, perr, perr}}
	h, store, seen := newNoSpaceHandler(t, mgr)
	e := noSpaceEntry()
	for i := 0; i < 5; i++ {
		_ = h.worker(context.Background(), 1, e, nil)
	}
	if len(store.logs) != 5 {
		t.Fatalf("logs = %d, want 5", len(store.logs))
	}
	for _, l := range store.logs {
		if l.Status != "error" || !l.KeepStreak {
			t.Fatalf("log = %+v, want error with KeepStreak", l)
		}
	}
	if len(store.ignoreCalls) != 0 || len(store.saved) != 0 {
		t.Fatalf("ignoreCalls = %v, saved = %d, want none", store.ignoreCalls, len(store.saved))
	}
	if k := seen(); len(k) != 0 {
		t.Fatalf("events = %v, want none", k)
	}
}
