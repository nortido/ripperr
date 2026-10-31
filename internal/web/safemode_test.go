package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/model"
	"ripperr/internal/runner"
	storagePkg "ripperr/internal/storage"
)

func safeEffective() config.App {
	c := testEffective()
	c.SafeMode = true
	return c
}

func safeServer(store *fakeStore, state *runner.State) *Server {
	return New(store, func() {}, safeEffective, ":0", state, nil)
}

func postRun(srv *Server, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	srv.handleRun(w, httptest.NewRequest(http.MethodPost, "/api/run", bytes.NewReader([]byte(body))))
	return w
}

func triggerQueued(state *runner.State) bool {
	select {
	case <-state.Trigger():
		return true
	default:
		return false
	}
}

func TestRunSafeModeRefusesCheck(t *testing.T) {
	state := runner.NewState()
	srv := safeServer(&fakeStore{}, state)

	w := postRun(srv, `{}`)
	if w.Code != http.StatusConflict || errCode(t, w) != "safe_mode" {
		t.Fatalf("normal: %d %s", w.Code, w.Body.String())
	}
	if triggerQueued(state) {
		t.Fatal("trigger queued in safe mode")
	}

	state.Begin(state.Reset())
	w = postRun(srv, `{"restart":true}`)
	if w.Code != http.StatusConflict || errCode(t, w) != "safe_mode" {
		t.Fatalf("restart: %d %s", w.Code, w.Body.String())
	}
	if state.ConsumeRestart() {
		t.Fatal("restart marked in safe mode")
	}
}

func TestRunDryOnlyInSafeMode(t *testing.T) {
	state := runner.NewState()
	srv := New(&fakeStore{}, func() {}, testEffective, ":0", state, nil)
	w := postRun(srv, `{"dry":true}`)
	if w.Code != http.StatusConflict || errCode(t, w) != "dry_run_off" {
		t.Fatalf("safe off: %d %s", w.Code, w.Body.String())
	}
	if triggerQueued(state) {
		t.Fatal("trigger queued with safe mode off")
	}

	state = runner.NewState()
	w = postRun(safeServer(&fakeStore{}, state), `{"dry":true}`)
	if w.Code != http.StatusAccepted {
		t.Fatalf("safe on: %d %s", w.Code, w.Body.String())
	}
	select {
	case dry := <-state.Trigger():
		if !dry {
			t.Fatal("expected dry flag on trigger")
		}
	default:
		t.Fatal("expected dry trigger queued")
	}
}

func TestRunStopAllowedInSafeMode(t *testing.T) {
	state := runner.NewState()
	srv := safeServer(&fakeStore{}, state)
	state.Begin(state.Reset())
	w := postRun(srv, `{"stop":true}`)
	if w.Code != http.StatusAccepted || !state.Stopping() {
		t.Fatalf("stop: %d %s stopping=%v", w.Code, w.Body.String(), state.Stopping())
	}
}

func TestStatusSafeMode(t *testing.T) {
	for _, on := range []bool{true, false} {
		eff := testEffective
		if on {
			eff = safeEffective
		}
		srv := New(&fakeStore{}, func() {}, eff, ":0", runner.NewState(), nil)
		w := httptest.NewRecorder()
		srv.handleStatus(w, httptest.NewRequest(http.MethodGet, "/api/status", nil))
		var resp map[string]any
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}
		if got, ok := resp["safe_mode"]; !ok || got != on {
			t.Fatalf("safe_mode = %v (present %v), want %v", got, ok, on)
		}
	}
}

func forcePost(srv *Server, force bool) (single, all *httptest.ResponseRecorder) {
	body := `{"force":false}`
	if force {
		body = `{"force":true}`
	}
	req := httptest.NewRequest(http.MethodPost, "/api/plan/abc/force", bytes.NewReader([]byte(body)))
	req.SetPathValue("hash", "abc")
	single = httptest.NewRecorder()
	srv.handlePlanForce(single, req)
	all = httptest.NewRecorder()
	srv.handlePlanForceAll(all, httptest.NewRequest(http.MethodPost, "/api/plan/force-all", bytes.NewReader([]byte(body))))
	return single, all
}

func TestPlanForceSafeMode(t *testing.T) {
	state := runner.NewState()
	store := &fakeStore{forceItems: map[string]bool{"abc": false}}
	srv := safeServer(store, state)

	single, all := forcePost(srv, true)
	for name, w := range map[string]*httptest.ResponseRecorder{"single": single, "all": all} {
		if w.Code != http.StatusConflict || errCode(t, w) != "safe_mode" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if store.forceItems["abc"] {
		t.Fatal("force flag written in safe mode")
	}
	select {
	case h := <-state.Force():
		t.Fatalf("RequestForce called for %q", h)
	default:
	}

	single, all = forcePost(srv, false)
	if single.Code != http.StatusOK || all.Code != http.StatusOK {
		t.Fatalf("force=false: single %d %s, all %d %s", single.Code, single.Body.String(), all.Code, all.Body.String())
	}
}

func TestRedownloadSafeMode(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()
	calls := 0
	managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
		calls++
		return &fakeQbitClient{}, nil
	}
	srv := safeServer(&fakeStore{}, runner.NewState())
	hash := storagePkg.URLHash("https://rutracker.org/forum/viewtopic.php?t=1")
	req := httptest.NewRequest(http.MethodPost, "/api/history/"+hash+"/redownload", nil)
	req.SetPathValue("hash", hash)
	w := httptest.NewRecorder()
	srv.handleRedownload(w, req)
	if w.Code != http.StatusConflict || errCode(t, w) != "safe_mode" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if calls != 0 {
		t.Fatalf("client factory called %d times", calls)
	}
}

func TestAddRemoveReleaseSafeModeCode(t *testing.T) {
	state := runner.NewState()
	state.SetReleaseActions(state.Reset(), &runner.ReleaseActions{
		Add: func(context.Context, runner.AddRequest) (runner.AddResult, error) {
			return runner.AddResult{}, runner.ErrSafeMode
		},
		Remove: func(context.Context, runner.RemoveRequest) error { return runner.ErrSafeMode },
	})
	srv := safeServer(&fakeStore{}, state)
	add := doRelease(srv.handleAddRelease, http.MethodPost, "/api/history/add", "", `{"url":"https://tr.example/t?id=1"}`)
	rem := doRelease(srv.handleRemoveRelease, http.MethodPost, "/api/history/5/remove", "5", `{"files":false,"torrents":1}`)
	for name, w := range map[string]*httptest.ResponseRecorder{"add": add, "remove": rem} {
		if w.Code != http.StatusConflict || errCode(t, w) != "safe_mode" {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
}
