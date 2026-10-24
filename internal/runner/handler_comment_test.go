package runner

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ripperr/internal/model"
	storagePkg "ripperr/internal/storage"
)

func (f *fakeManager) AttachComment(_ context.Context, hash, comment string, _ func(context.Context) ([]byte, error)) error {
	f.attached = append(f.attached, hash+"|"+comment)
	return nil
}

func TestAdoptionAttachesCommentOnce(t *testing.T) {
	const url = "https://aniliberty.top/anime/releases/release/x"
	run := func(h *handler, dry bool) {
		t.Helper()
		if err := h.push(context.Background(), dry, nil); err != nil {
			t.Fatal(err)
		}
		for len(h.idx[1]) > 0 {
			<-h.idx[1]
		}
	}
	newHandler := func() (*handler, *fakeManager) {
		mgr := &fakeManager{all: []*model.Entry{{Name: "X - AniLiberty [WEB-DL]", Hash: "h1"}}}
		h := &handler{manager: mgr, storage: &fakeStorage{}, trackerService: &adoptTracker{fakeTracker: fakeTracker{id: 1}, url: url}, state: NewState()}
		h.initTrackerChannels()
		return h, mgr
	}

	h, mgr := newHandler()
	run(h, false)
	mgr.all[0].URL = ""
	run(h, false)
	if len(mgr.attached) != 1 || mgr.attached[0] != "h1|"+url {
		t.Fatalf("attached = %v, want one call for h1", mgr.attached)
	}

	h, mgr = newHandler()
	run(h, true)
	if len(mgr.attached) != 0 {
		t.Fatalf("dry run attached %v", mgr.attached)
	}
}

type adoptByHashTracker struct {
	fakeTracker
	urls  map[string]string
	calls []string
}

func (a *adoptByHashTracker) Adopt(_ context.Context, _, hash string) (string, error) {
	a.calls = append(a.calls, hash)
	return a.urls[hash], nil
}

func TestAttachOnlyForQueuedEntries(t *testing.T) {
	const base = "https://aniliberty.top/anime/releases/release/"
	newHandler := func() (*handler, *fakeManager, *adoptByHashTracker) {
		mgr := &fakeManager{all: []*model.Entry{
			{Name: "other category", Hash: "h1", Category: "music", Tag: "watch"},
			{Name: "no watch tag", Hash: "h2", Category: "anime", Tag: "seed"},
			{Name: "ignored", Hash: "h3", Category: "anime", Tag: "watch"},
			{Name: "queued", Hash: "h4", Category: "anime", Tag: "watch"},
		}}
		tr := &adoptByHashTracker{fakeTracker: fakeTracker{id: 1}, urls: map[string]string{
			"h1": base + "a", "h2": base + "b", "h3": base + "c", "h4": base + "d",
		}}
		h := &handler{
			config:         model.HandlerConfig{Categories: []string{"anime"}, WatchTags: []string{"watch"}},
			manager:        mgr,
			storage:        &fakeStorage{ignore: map[string]struct{}{storagePkg.URLHash(base + "c"): {}}},
			trackerService: tr,
			state:          NewState(),
		}
		h.initTrackerChannels()
		return h, mgr, tr
	}

	h, mgr, tr := newHandler()
	if err := h.push(context.Background(), false, nil); err != nil {
		t.Fatal(err)
	}
	if len(mgr.attached) != 1 || mgr.attached[0] != "h4|"+base+"d" {
		t.Fatalf("attached = %v, want only h4", mgr.attached)
	}
	if len(tr.calls) != 4 {
		t.Fatalf("Adopt calls = %v, want all four: history purge needs every URL", tr.calls)
	}

	h, mgr, _ = newHandler()
	forced := map[string]struct{}{storagePkg.URLHash(base + "c"): {}}
	if err := h.push(context.Background(), false, forced); err != nil {
		t.Fatal(err)
	}
	if len(mgr.attached) != 1 || mgr.attached[0] != "h3|"+base+"c" {
		t.Fatalf("forced attached = %v, want only the forced h3", mgr.attached)
	}
}

type lockProbeManager struct {
	fakeManager
	attachIn, attachGate chan struct{}
	submitIn, submitGate chan struct{}
	attachCalls          atomic.Int32
	submitCalls          atomic.Int32
}

func (m *lockProbeManager) AttachComment(context.Context, string, string, func(context.Context) ([]byte, error)) error {
	m.attachCalls.Add(1)
	if m.attachGate != nil {
		close(m.attachIn)
		<-m.attachGate
	}
	return nil
}

func (m *lockProbeManager) Submit(context.Context, *bytes.Buffer, model.ManagerAddFileOptions) error {
	m.submitCalls.Add(1)
	if m.submitGate != nil {
		close(m.submitIn)
		<-m.submitGate
	}
	return nil
}

func newLockProbeHandler() (*handler, *lockProbeManager, *model.Entry) {
	mgr := &lockProbeManager{}
	h := &handler{
		config:  model.HandlerConfig{ThresholdMonth: 1, ThresholdHalfYear: 1, ThresholdYear: 1, ThresholdOld: 1},
		storage: &loggingStorage{},
		manager: mgr,
		trackerService: &updatingPageTracker{
			pageTracker: pageTracker{safeFakeTracker: safeFakeTracker{id: 1}, page: bytes.NewBufferString(strings.Repeat("x", 60))},
		},
		state: NewState(),
	}
	old := time.Now().Add(-48 * time.Hour)
	e := &model.Entry{URL: "https://example.com/t/1", Name: "test", URLHash: "abc", Hash: "AbC", Timestamp: &old}
	return h, mgr, e
}

func TestWorkerWaitsForAttachComment(t *testing.T) {
	h, mgr, e := newLockProbeHandler()
	mgr.attachIn, mgr.attachGate = make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(mgr.attachGate) })
	defer release()
	go h.attachComment(context.Background(), e)
	<-mgr.attachIn

	done := make(chan error, 1)
	go func() { done <- h.worker(context.Background(), 1, e, nil) }()
	time.Sleep(100 * time.Millisecond)
	if got := mgr.submitCalls.Load(); got != 0 {
		t.Fatalf("Submit called %d times while AttachComment of the same hash is running", got)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := mgr.submitCalls.Load(); got != 1 {
		t.Fatalf("Submit calls = %d after AttachComment returned, want 1", got)
	}
}

func TestAttachCommentSkipsWhileWorkerBusy(t *testing.T) {
	h, mgr, e := newLockProbeHandler()
	mgr.submitIn, mgr.submitGate = make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(mgr.submitGate) })
	defer release()
	done := make(chan error, 1)
	go func() { done <- h.worker(context.Background(), 1, e, nil) }()
	<-mgr.submitIn

	returned := make(chan struct{})
	go func() {
		h.attachComment(context.Background(), e)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("attachComment blocked behind a running replace")
	}
	if got := mgr.attachCalls.Load(); got != 0 {
		t.Fatalf("AttachComment called %d times while a replace of the same hash runs", got)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	h.attachComment(context.Background(), e)
	if got := mgr.attachCalls.Load(); got != 1 {
		t.Fatalf("AttachComment calls = %d after the replace finished, want 1 (the skipped call must not be marked done)", got)
	}
}
