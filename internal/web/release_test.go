package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/model"
	"ripperr/internal/runner"
)

type catsClient struct {
	cats []string
	err  error
}

func (c *catsClient) GetCategories(context.Context) ([]string, error) { return c.cats, c.err }

func (c *catsClient) List(context.Context, model.FilterOptions) ([]*model.Entry, error) {
	return nil, nil
}

func (c *catsClient) ForceRedownload(context.Context, string, model.RedownloadOptions) error {
	return nil
}

func releaseServer(t *testing.T, a *runner.ReleaseActions) *Server {
	t.Helper()
	state := runner.NewState()
	if a != nil {
		state.SetReleaseActions(state.Reset(), a)
	}
	return New(&fakeStore{}, func() {}, testEffective, ":0", state, nil)
}

func doRelease(h http.HandlerFunc, method, target, hash, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if hash != "" {
		req.SetPathValue("hash", hash)
	}
	w := httptest.NewRecorder()
	h(w, req)
	return w
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var got struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %q: %v", w.Body.String(), err)
	}
	if got.Error == "" {
		t.Fatalf("no error message in %q", w.Body.String())
	}
	return got.Code
}

func TestAddOptions(t *testing.T) {
	orig := managerClientFactory
	defer func() { managerClientFactory = orig }()

	cases := []struct {
		name    string
		runner  config.Runner
		client  string
		factory func(string, model.ManagerConfig) (managerClient, error)
		want    string
	}{
		{"categories", config.Runner{WatchMode: "categories", AllowedCategories: []string{"tv", "movies"}}, "qbit", nil,
			`{"mode":"categories","options":["tv","movies"],"any":false}`},
		{"tags qbit", config.Runner{WatchMode: "tags", WatchTags: []string{"watch"}}, "qbit", nil,
			`{"mode":"tags","options":["watch"],"any":false}`},
		{"tags with deluge fall back to categories", config.Runner{WatchMode: "tags", WatchTags: []string{"watch"}, AllowedCategories: []string{"tv"}}, "deluge", nil,
			`{"mode":"categories","options":["tv"],"any":false}`},
		{"empty categories list client ones", config.Runner{WatchMode: "categories"}, "qbit",
			func(string, model.ManagerConfig) (managerClient, error) {
				return &catsClient{cats: []string{"a", "b"}}, nil
			},
			`{"mode":"categories","options":["a","b"],"any":true}`},
		{"factory error", config.Runner{WatchMode: "categories"}, "qbit",
			func(string, model.ManagerConfig) (managerClient, error) { return nil, errors.New("down") },
			`{"mode":"categories","options":[],"any":true}`},
		{"client categories error", config.Runner{WatchMode: "categories"}, "qbit",
			func(string, model.ManagerConfig) (managerClient, error) {
				return &catsClient{cats: []string{"x"}, err: errors.New("down")}, nil
			},
			`{"mode":"categories","options":[],"any":true}`},
		{"empty tags", config.Runner{WatchMode: "tags"}, "qbit", nil,
			`{"mode":"tags","options":[],"any":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			managerClientFactory = tc.factory
			if tc.factory == nil {
				managerClientFactory = func(string, model.ManagerConfig) (managerClient, error) {
					t.Fatal("client must not be asked")
					return nil, nil
				}
			}
			srv := releaseServer(t, nil)
			srv.effective = func() config.App {
				cfg := testEffective()
				cfg.Runner = tc.runner
				cfg.Manager.Client = tc.client
				return cfg
			}
			w := doRelease(srv.handleAddOptions, http.MethodGet, "/api/history/add", "", "")
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if got := strings.TrimSpace(w.Body.String()); got != tc.want {
				t.Fatalf("body %s, want %s", got, tc.want)
			}
		})
	}
}

func TestAddReleaseValidation(t *testing.T) {
	calls := 0
	srv := releaseServer(t, &runner.ReleaseActions{Add: func(context.Context, runner.AddRequest) (runner.AddResult, error) {
		calls++
		return runner.AddResult{}, nil
	}})
	long := `{"url":"https://tr.example/` + strings.Repeat("a", 2049) + `"}`
	cases := []struct{ name, body, code string }{
		{"magnet", `{"url":"magnet:?xt=urn:btih:abc"}`, "bad_url"},
		{"ftp", `{"url":"ftp://x/t"}`, "bad_url"},
		{"empty", `{"url":""}`, "bad_url"},
		{"blank", `{"url":"   "}`, "bad_url"},
		{"no host", `{"url":"http://"}`, "bad_url"},
		{"too long", long, "bad_url"},
		{"unknown field", `{"url":"https://tr.example/t","extra":1}`, ""},
		{"not json", `nope`, ""},
		{"long category", `{"url":"https://tr.example/t","category":"` + strings.Repeat("c", 257) + `"}`, ""},
		{"long tag", `{"url":"https://tr.example/t","tag":"` + strings.Repeat("c", 257) + `"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRelease(srv.handleAddRelease, http.MethodPost, "/api/history/add", "", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if got := errCode(t, w); got != tc.code {
				t.Fatalf("code %q, want %q", got, tc.code)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("hook called %d times", calls)
	}
}

var releaseErrCases = []struct {
	err    error
	status int
	code   string
}{
	{runner.ErrPollUnavailable, 503, "runner_unavailable"},
	{runner.ErrNotWatched, 400, "not_watched"},
	{runner.ErrUnknownTracker, 422, "tracker_unknown"},
	{runner.ErrNoTorrent, 422, "no_torrent"},
	{fmt.Errorf("add: %w", model.ErrAlreadyAdded), 409, "already_added"},
	{runner.ErrReleaseBusy, 409, "torrent_busy"},
	{model.ErrReplacePending, 409, "replace_pending"},
	{runner.ErrTorrentsChanged, 409, "torrents_changed"},
	{model.ErrFilesNotAllowed, 409, "files_not_allowed"},
	{model.ErrPartialList, 502, "partial_list"},
	{model.ErrRemoveUnconfirmed, 502, "remove_unconfirmed"},
	{context.DeadlineExceeded, 504, "timeout"},
	{errors.New("boom"), 502, ""},
}

func TestAddReleaseForwardsAndMapsErrors(t *testing.T) {
	var got runner.AddRequest
	var fail error
	srv := releaseServer(t, &runner.ReleaseActions{Add: func(_ context.Context, r runner.AddRequest) (runner.AddResult, error) {
		got = r
		return runner.AddResult{Unignored: true}, fail
	}})
	post := func() *httptest.ResponseRecorder {
		return doRelease(srv.handleAddRelease, http.MethodPost, "/api/history/add", "", `{"url":"  https://tr.example/t?id=1 ","category":" tv ","tag":" x "}`)
	}

	w := post()
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ok":true,"unignored":true}` {
		t.Fatalf("success: %d %s", w.Code, w.Body.String())
	}
	if want := (runner.AddRequest{URL: "https://tr.example/t?id=1", Category: "tv", Tag: "x"}); got != want {
		t.Fatalf("hook got %+v, want %+v", got, want)
	}
	for _, tc := range releaseErrCases {
		fail = tc.err
		w := post()
		if w.Code != tc.status || errCode(t, w) != tc.code {
			t.Fatalf("%v: %d %s, want %d %q", tc.err, w.Code, w.Body.String(), tc.status, tc.code)
		}
	}
}

func TestReleaseActionsUnavailable(t *testing.T) {
	srv := releaseServer(t, nil)
	cases := []struct {
		name string
		w    *httptest.ResponseRecorder
	}{
		{"add", doRelease(srv.handleAddRelease, http.MethodPost, "/api/history/add", "", `{"url":"https://tr.example/t"}`)},
		{"removal", doRelease(srv.handleRemovalInfo, http.MethodGet, "/api/history/5/remove", "5", "")},
		{"remove", doRelease(srv.handleRemoveRelease, http.MethodPost, "/api/history/5/remove", "5", `{"files":false,"torrents":1}`)},
	}
	for _, tc := range cases {
		if tc.w.Code != http.StatusServiceUnavailable || errCode(t, tc.w) != "runner_unavailable" {
			t.Fatalf("%s: %d %s", tc.name, tc.w.Code, tc.w.Body.String())
		}
	}
}

func TestRemovalInfoAndRemove(t *testing.T) {
	var info runner.RemovalInfo
	var gotHash string
	var req runner.RemoveRequest
	var fail error
	srv := releaseServer(t, &runner.ReleaseActions{
		Removal: func(_ context.Context, h string) (runner.RemovalInfo, error) {
			gotHash = h
			return info, fail
		},
		Remove: func(_ context.Context, r runner.RemoveRequest) error {
			req = r
			return fail
		},
	})
	info = runner.RemovalInfo{Torrents: 2, Files: true}

	w := doRelease(srv.handleRemovalInfo, http.MethodGet, "/api/history/42/remove", "42", "")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"torrents":2,"files":true}` || gotHash != "42" {
		t.Fatalf("info: %d %s hash %q", w.Code, w.Body.String(), gotHash)
	}
	for _, h := range []string{"abc", "-1", "4294967296", "1.5"} {
		if w := doRelease(srv.handleRemovalInfo, http.MethodGet, "/x", h, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("info hash %q: %d", h, w.Code)
		}
		if w := doRelease(srv.handleRemoveRelease, http.MethodPost, "/x", h, `{"files":false,"torrents":1}`); w.Code != http.StatusBadRequest {
			t.Fatalf("remove hash %q: %d", h, w.Code)
		}
	}

	w = doRelease(srv.handleRemoveRelease, http.MethodPost, "/api/history/42/remove", "42", `{"files":true,"torrents":2}`)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ok":true}` {
		t.Fatalf("remove: %d %s", w.Code, w.Body.String())
	}
	if want := (runner.RemoveRequest{URLHash: "42", Files: true, Torrents: 2}); !reflect.DeepEqual(req, want) {
		t.Fatalf("request %+v, want %+v", req, want)
	}
	w = doRelease(srv.handleRemoveRelease, http.MethodPost, "/x", "42", `{"torrents":0}`)
	if w.Code != http.StatusOK || req.Torrents != 0 || req.Files {
		t.Fatalf("zero torrents: %d %+v", w.Code, req)
	}

	for _, body := range []string{`{"files":true}`, `{"files":true,"torrents":-1}`, `{"torrents":null}`, `{"torrents":1,"x":1}`, ``} {
		req = runner.RemoveRequest{}
		if w := doRelease(srv.handleRemoveRelease, http.MethodPost, "/x", "42", body); w.Code != http.StatusBadRequest || req != (runner.RemoveRequest{}) {
			t.Fatalf("body %q: %d %+v", body, w.Code, req)
		}
	}

	for _, tc := range releaseErrCases {
		fail = tc.err
		for name, w := range map[string]*httptest.ResponseRecorder{
			"info":   doRelease(srv.handleRemovalInfo, http.MethodGet, "/x", "42", ""),
			"remove": doRelease(srv.handleRemoveRelease, http.MethodPost, "/x", "42", `{"torrents":1}`),
		} {
			if w.Code != tc.status || errCode(t, w) != tc.code {
				t.Fatalf("%s %v: %d %s, want %d %q", name, tc.err, w.Code, w.Body.String(), tc.status, tc.code)
			}
		}
	}
}

func TestReleaseRoutesRegistered(t *testing.T) {
	post := func(srv *Server, target, body string, csrf bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		if csrf {
			req.Header.Set("X-Ripperr", "1")
		}
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		return w
	}
	bare := newBareServer(&fakeStore{})
	for _, target := range []string{"/api/history/add", "/api/history/7/remove"} {
		if w := post(bare, target, `{}`, false); w.Code != http.StatusForbidden {
			t.Fatalf("%s without X-Ripperr: %d", target, w.Code)
		}
	}

	srv := withTestSession(newBareServer(&fakeStore{}))
	if w := post(srv, "/api/history/add", `{}`, true); w.Code != http.StatusBadRequest {
		t.Fatalf("add bad body: %d %s", w.Code, w.Body.String())
	}
	if w := post(srv, "/api/history/7/remove", `{}`, true); w.Code != http.StatusBadRequest {
		t.Fatalf("remove bad body: %d %s", w.Code, w.Body.String())
	}
	for _, target := range []string{"/api/history/add", "/api/history/7/remove"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound || w.Code == http.StatusMethodNotAllowed {
			t.Fatalf("GET %s not routed: %d", target, w.Code)
		}
	}
}
