package qbittorrent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	qbt "github.com/autobrr/go-qbittorrent"

	"ripperr/internal/model"
)

func newStubServer(t *testing.T, torrents []qbt.Torrent, propsHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("4.6.0"))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(torrents)
	})
	mux.HandleFunc("/api/v2/torrents/properties", propsHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestList_Success(t *testing.T) {
	torrents := []qbt.Torrent{
		{Hash: "h1", Name: "torrent-1", Category: "movies"},
		{Hash: "h2", Name: "torrent-2", Category: "movies"},
	}
	server := newStubServer(t, torrents, func(w http.ResponseWriter, r *http.Request) {
		hash := r.URL.Query().Get("hash")
		_ = json.NewEncoder(w).Encode(qbt.TorrentProperties{
			Hash:         hash,
			Comment:      "https://example.com/topic/" + strings.TrimPrefix(hash, "h"),
			AdditionDate: 1700000000,
		})
	})

	c, err := New(model.ManagerConfig{Host: server.URL})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	entries, err := c.List(context.Background(), model.FilterOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Hash != "h1" || entries[1].Hash != "h2" {
		t.Errorf("expected entries in request order, got %+v / %+v", entries[0], entries[1])
	}
	if entries[0].TopicID != "1" || entries[1].TopicID != "2" {
		t.Errorf("expected topic IDs extracted from comment, got %q / %q", entries[0].TopicID, entries[1].TopicID)
	}
}

func TestList_SkipsBadTorrent(t *testing.T) {
	torrents := []qbt.Torrent{
		{Hash: "h1", Name: "torrent-1"},
		{Hash: "h2", Name: "torrent-2"},
		{Hash: "h3", Name: "torrent-3"},
	}
	server := newStubServer(t, torrents, func(w http.ResponseWriter, r *http.Request) {
		hash := r.URL.Query().Get("hash")
		if hash == "h2" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(qbt.TorrentProperties{
			Hash:         hash,
			Comment:      "https://example.com/topic/" + strings.TrimPrefix(hash, "h"),
			AdditionDate: 1700000000,
		})
	})

	c, err := New(model.ManagerConfig{Host: server.URL})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	entries, err := c.List(context.Background(), model.FilterOptions{})
	if !errors.Is(err, model.ErrPartialList) {
		t.Fatalf("expected ErrPartialList, got: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e == nil {
			t.Fatal("expected no nil entries")
		}
		if e.Hash == "h2" {
			t.Fatalf("unexpected failed torrent in result: %+v", e)
		}
	}
}

func TestList_ContextCancellationDoesNotHang(t *testing.T) {
	unblock := make(chan struct{})
	defer close(unblock)

	torrents := []qbt.Torrent{
		{Hash: "h1", Name: "torrent-1"},
		{Hash: "h2", Name: "torrent-2"},
	}
	server := newStubServer(t, torrents, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-unblock:
		}
	})

	c, err := New(model.ManagerConfig{Host: server.URL})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := c.List(ctx, model.FilterOptions{})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error once the context times out")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("List did not return after context cancellation: possible hang")
	}
}

func buildTestTorrent(t *testing.T) ([]byte, string) {
	t.Helper()
	info := metainfo.Info{
		PieceLength: 16384,
		Pieces:      make([]byte, 20),
		Name:        "test.txt",
		Length:      0,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{
		InfoBytes: infoBytes,
		Announce:  "http://tracker.example/announce",
	}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes(), mi.HashInfoBytes().HexString()
}

func TestAddFromBuffer_WaitsForDeleteRace(t *testing.T) {
	data, hash := buildTestTorrent(t)
	var infoCalls atomic.Int32
	var addCalled atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("4.6.0"))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		n := infoCalls.Add(1)
		if n <= 2 {
			_ = json.NewEncoder(w).Encode([]qbt.Torrent{{Hash: hash, Name: "old"}})
			return
		}
		if addCalled.Load() {
			_ = json.NewEncoder(w).Encode([]qbt.Torrent{{Hash: hash, Name: "new"}})
			return
		}
		_ = json.NewEncoder(w).Encode([]qbt.Torrent{})
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		addCalled.Store(true)
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c, err := New(model.ManagerConfig{Host: server.URL, DeleteEnabled: true})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	opts := model.ManagerAddFileOptions{Hash: strings.ToUpper(hash), Name: "new"}
	err = c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !addCalled.Load() {
		t.Fatal("expected torrent add API to be called")
	}
	if infoCalls.Load() < 3 {
		t.Fatalf("expected at least 3 info polls (wait for delete), got %d", infoCalls.Load())
	}
}

func TestGetClientNeverProxiesTorrentClient(t *testing.T) {
	// прокси из окружения увел бы пароль от веб-морды его владельцу, а клиент и так стоит рядом
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:3128")
	srv := newStubServer(t, nil, func(http.ResponseWriter, *http.Request) {})

	c := &Client{config: model.ManagerConfig{Host: srv.URL, Username: "u", Password: "p"}}
	// ошибка логина тут ни при чем, транспорт ставится раньше
	_ = c.getClient()
	tr, ok := c.client.GetHTTPClient().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("want *http.Transport, got %T", c.client.GetHTTPClient().Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("qbit client still resolves a proxy")
	}
}

func newAddRecorder(t *testing.T, hash string) (*Client, *map[string]string) {
	t.Helper()
	var added atomic.Bool
	var mu sync.Mutex
	got := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("4.6.0"))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		if added.Load() {
			_ = json.NewEncoder(w).Encode([]qbt.Torrent{{Hash: hash, Name: "new"}})
			return
		}
		_ = json.NewEncoder(w).Encode([]qbt.Torrent{})
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(32 << 20)
		mu.Lock()
		for _, k := range []string{"autoTMM", "savepath", "category", "stopped", "paused"} {
			got[k] = r.FormValue(k)
		}
		mu.Unlock()
		added.Store(true)
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c, err := New(model.ManagerConfig{Host: server.URL})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}
	return c, &got
}

func TestAddFromBuffer_AutoManagedSendsAutoTMMTrue(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, got := newAddRecorder(t, hash)

	opts := model.ManagerAddFileOptions{Name: "new", Category: "movies", SavePath: "/data/movies", AutoManaged: true}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if (*got)["autoTMM"] != "true" || (*got)["savepath"] != "" || (*got)["category"] != "movies" {
		t.Fatalf("expected autoTMM=true, no savepath, category movies; got %+v", *got)
	}
}

func TestAddFromBuffer_ManualKeepsSavePath(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, got := newAddRecorder(t, hash)

	opts := model.ManagerAddFileOptions{Name: "new", SavePath: "/data/x"}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if (*got)["autoTMM"] != "false" || (*got)["savepath"] != "/data/x" {
		t.Fatalf("expected autoTMM=false and savepath /data/x; got %+v", *got)
	}
}

func TestAddFromMagnet_URLPathSendsAutoTMM(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef01234567"
	c, got := newAddRecorder(t, hash)

	opts := model.ManagerAddFileOptions{Name: "new", AutoManaged: true}
	if err := c.AddFromMagnet(context.Background(), "magnet:?xt=urn:btih:"+hash, opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if (*got)["autoTMM"] != "true" {
		t.Fatalf("expected autoTMM=true; got %+v", *got)
	}
}

func TestList_MapsSavePath(t *testing.T) {
	server := newStubServer(t, []qbt.Torrent{{Hash: "h1", Name: "t", SavePath: "/data/movies"}}, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(qbt.TorrentProperties{Hash: "h1", AdditionDate: 1700000000})
	})
	c, err := New(model.ManagerConfig{Host: server.URL})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}
	entries, err := c.List(context.Background(), model.FilterOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 || entries[0].SavePath != "/data/movies" {
		t.Fatalf("expected SavePath /data/movies, got %+v", entries)
	}
}

func TestDeletesFiles(t *testing.T) {
	cases := []struct {
		name     string
		cfg      model.ManagerConfig
		category string
		want     bool
	}{
		{"all on, listed", model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"tv", "movies"}}, "movies", true},
		{"category not listed", model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"tv"}}, "movies", false},
		{"files disabled", model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}}, "movies", false},
		{"delete disabled", model.ManagerConfig{DeleteFilesEnabled: true, DeleteCategories: []string{"movies"}}, "movies", false},
		{"no categories", model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{config: tc.cfg}
			if got := c.DeletesFiles(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: tc.category}); got != tc.want {
				t.Fatalf("DeletesFiles = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDelete_ListedCategoryPostsDeleteFiles(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("4.6.0"))
	})
	mux.HandleFunc("/api/v2/torrents/delete", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		got["hashes"] = r.FormValue("hashes")
		got["deleteFiles"] = r.FormValue("deleteFiles")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c, err := New(model.ManagerConfig{Host: server.URL, DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"movies"}})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}
	if err = c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "movies", Name: "x", DeleteFiles: true}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got["hashes"] != "h1" || got["deleteFiles"] != "true" {
		t.Fatalf("expected hashes=h1 deleteFiles=true, got %+v", got)
	}
}

func TestAddFromBuffer_StoppedFlagSendsStoppedAndPaused(t *testing.T) {
	for _, stopped := range []bool{true, false} {
		data, hash := buildTestTorrent(t)
		c, got := newAddRecorder(t, hash)
		opts := model.ManagerAddFileOptions{Name: "new", Stopped: stopped}
		if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := strconv.FormatBool(stopped)
		if (*got)["stopped"] != want || (*got)["paused"] != want {
			t.Errorf("Stopped %v: stopped=%q paused=%q, want %q", stopped, (*got)["stopped"], (*got)["paused"], want)
		}
	}
}

func TestDiscard_NeverDeletesFiles(t *testing.T) {
	cfgs := map[string]model.ManagerConfig{
		"delete files on": {DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"movies"}},
		"delete disabled": {},
	}
	for name, cfg := range cfgs {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			got := map[string]string{}
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v2/torrents/delete", func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				mu.Lock()
				got["hashes"] = r.FormValue("hashes")
				got["deleteFiles"] = r.FormValue("deleteFiles")
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
			})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			cfg.Host = server.URL
			c, err := New(cfg)
			if err != nil {
				t.Fatalf("unexpected error creating client: %v", err)
			}
			if err = c.Discard(context.Background(), "h1"); err != nil {
				t.Fatalf("Discard: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if got["hashes"] != "h1" || got["deleteFiles"] != "false" {
				t.Fatalf("expected hashes=h1 deleteFiles=false, got %+v", got)
			}
		})
	}
}

func TestPurge_DeletesFilesWhateverSettings(t *testing.T) {
	cfgs := map[string]model.ManagerConfig{
		"delete disabled":     {},
		"category not listed": {DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"movies"}},
	}
	for name, cfg := range cfgs {
		t.Run(name, func(t *testing.T) {
			var mu sync.Mutex
			got := map[string]string{}
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v2/torrents/delete", func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				mu.Lock()
				got["hashes"] = r.FormValue("hashes")
				got["deleteFiles"] = r.FormValue("deleteFiles")
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
			})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			cfg.Host = server.URL
			c, err := New(cfg)
			if err != nil {
				t.Fatalf("unexpected error creating client: %v", err)
			}
			if err = c.Purge(context.Background(), "h1"); err != nil {
				t.Fatalf("Purge: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if got["hashes"] != "h1" || got["deleteFiles"] != "true" {
				t.Fatalf("expected hashes=h1 deleteFiles=true, got %+v", got)
			}
		})
	}
}

func withFastCheckWait(t *testing.T, budget time.Duration) {
	t.Helper()
	origDelay, origBudget := checkWaitDelay, checkWaitBudget
	checkWaitDelay, checkWaitBudget = 5*time.Millisecond, budget
	t.Cleanup(func() { checkWaitDelay, checkWaitBudget = origDelay, origBudget })
}

func newResumeServer(t *testing.T, before, after []qbt.TorrentState) (*Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var endpoints []string
	started := false
	next := func(states *[]qbt.TorrentState) qbt.TorrentState {
		s := (*states)[0]
		if len(*states) > 1 {
			*states = (*states)[1:]
		}
		return s
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("2.11.2"))
	})
	mux.HandleFunc("/api/v2/torrents/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		name := strings.TrimPrefix(r.URL.Path, "/api/v2/torrents/")
		endpoints = append(endpoints, name)
		switch name {
		case "start", "resume":
			started = true
		case "info":
			state := next(&before)
			if started {
				state = next(&after)
			}
			_ = json.NewEncoder(w).Encode([]qbt.Torrent{{Hash: "h1", State: state}})
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	c, err := New(model.ManagerConfig{Host: server.URL})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), endpoints...)
	}
}

func states(s ...qbt.TorrentState) []qbt.TorrentState { return s }

func TestResume_StartsWithoutRecheckAndWaitsPastStaleState(t *testing.T) {
	// состояние в webapi кешируется: первые чтения после start еще stoppedDL
	withFastCheckWait(t, time.Second)
	c, endpoints := newResumeServer(t, states(qbt.TorrentStateStoppedDl), states(qbt.TorrentStateStoppedDl, qbt.TorrentStateStoppedDl, qbt.TorrentStateDownloading))
	if err := c.Resume(context.Background(), "h1"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	want := []string{"info", "start", "info", "info", "info"}
	if got := endpoints(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("endpoints = %v, want %v", got, want)
	}
}

func TestResume_StillStoppedAfterStartIsError(t *testing.T) {
	withFastCheckWait(t, 50*time.Millisecond)
	c, _ := newResumeServer(t, states(qbt.TorrentStateStoppedDl), states(qbt.TorrentStateStoppedDl))
	if err := c.Resume(context.Background(), "h1"); err == nil {
		t.Fatal("expected an error for a torrent that stays stopped")
	}
}

func TestResume_AlreadyRunningNoStart(t *testing.T) {
	withFastCheckWait(t, time.Second)
	c, endpoints := newResumeServer(t, states(qbt.TorrentStateDownloading), states(qbt.TorrentStateDownloading))
	if err := c.Resume(context.Background(), "h1"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := endpoints(); strings.Join(got, ",") != "info" {
		t.Fatalf("endpoints = %v, want [info]", got)
	}
}

func TestResume_CheckPastBudgetNeverStarts(t *testing.T) {
	withFastCheckWait(t, 50*time.Millisecond)
	c, endpoints := newResumeServer(t, states(qbt.TorrentStateCheckingResumeData), states(qbt.TorrentStateDownloading))
	if err := c.Resume(context.Background(), "h1"); err == nil {
		t.Fatal("expected a timeout error")
	}
	for _, e := range endpoints() {
		if e == "start" || e == "resume" || e == "recheck" {
			t.Fatalf("unexpected %s: %v", e, endpoints())
		}
	}
}

func TestResume_MissingFilesAfterStartIsError(t *testing.T) {
	withFastCheckWait(t, time.Second)
	c, _ := newResumeServer(t, states(qbt.TorrentStateStoppedDl), states(qbt.TorrentStateMissingFiles))
	if err := c.Resume(context.Background(), "h1"); err == nil || !strings.Contains(err.Error(), "missingFiles") {
		t.Fatalf("err = %v, want missingFiles error", err)
	}
}

func TestMapEntry_Unhealthy(t *testing.T) {
	c := &Client{}
	unhealthy := []qbt.TorrentState{qbt.TorrentStateError, qbt.TorrentStateMissingFiles, qbt.TorrentStateCheckingUp, qbt.TorrentStateCheckingDl, qbt.TorrentStateCheckingResumeData, qbt.TorrentStateMoving}
	healthy := []qbt.TorrentState{qbt.TorrentStateUploading, qbt.TorrentStateStalledUp, qbt.TorrentStateDownloading, qbt.TorrentStateStoppedUp, qbt.TorrentStatePausedDl, qbt.TorrentStateQueuedDl}
	for _, s := range unhealthy {
		if !c.mapEntry(&qbt.Torrent{State: s}, &qbt.TorrentProperties{}).Unhealthy {
			t.Errorf("state %s: Unhealthy = false, want true", s)
		}
	}
	for _, s := range healthy {
		if c.mapEntry(&qbt.Torrent{State: s}, &qbt.TorrentProperties{}).Unhealthy {
			t.Errorf("state %s: Unhealthy = true, want false", s)
		}
	}
}

func TestNewErrorHidesHostPassword(t *testing.T) {
	for _, host := range []string{"http://u:SEC-qh@127.0.0.1:1", "http://u:SEC-q*h@127.0.0.1:1"} {
		_, err := New(model.ManagerConfig{Host: host, Username: "u", Password: "p"})
		if err == nil {
			t.Fatalf("%s: want a connection error", host)
		}
		if msg := err.Error(); strings.Contains(msg, "SEC-") || !strings.Contains(msg, "127.0.0.1:1") {
			t.Errorf("%s: error = %q, want host without password", host, msg)
		}
	}
}

func TestLaterCallErrorsHideHostPassword(t *testing.T) {
	var noBasic atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "u" || p != "SEC-qh" {
			noBasic.Add(1)
		}
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	host := "http://u:SEC-qh@" + strings.TrimPrefix(server.URL, "http://")
	c, err := New(model.ManagerConfig{Host: host, Username: "admin", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.GetCategories(context.Background()); err == nil {
		t.Fatal("want status error")
	}
	server.Close()
	_, err = c.GetCategories(context.Background())
	if err == nil {
		t.Fatal("want transport error")
	}
	if strings.Contains(err.Error(), "SEC-") {
		t.Errorf("error leaks host password: %q", err.Error())
	}
	if n := noBasic.Load(); n != 0 {
		t.Errorf("%d requests without basic auth from host userinfo", n)
	}
}

func TestLaterCallErrorsHideHostPasswordEmptyUser(t *testing.T) {
	cases := []struct{ userinfo, wantUser, wantPass string }{
		{":SEC-qe@", "", "SEC-qe"},
		{"u@", "u", ""},
	}
	for _, tc := range cases {
		t.Run(tc.userinfo, func(t *testing.T) {
			var noBasic atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if u, p, ok := r.BasicAuth(); !ok || u != tc.wantUser || p != tc.wantPass {
					noBasic.Add(1)
				}
				if r.URL.Path == "/api/v2/auth/login" {
					http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid", Path: "/"})
					_, _ = w.Write([]byte("Ok."))
					return
				}
				w.WriteHeader(http.StatusInternalServerError)
			}))
			host := "http://" + tc.userinfo + strings.TrimPrefix(server.URL, "http://")
			c, err := New(model.ManagerConfig{Host: host, Username: "admin", Password: "p"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = c.GetCategories(context.Background()); err == nil {
				t.Fatal("want status error")
			}
			server.Close()
			_, err = c.GetCategories(context.Background())
			if err == nil {
				t.Fatal("want transport error")
			}
			if strings.Contains(err.Error(), "SEC-") {
				t.Errorf("error leaks host password: %q", err.Error())
			}
			if n := noBasic.Load(); n != 0 {
				t.Errorf("%d requests without expected basic auth from host userinfo", n)
			}
		})
	}
}

func TestEmptyUserBasicNotSentOnCrossHostRedirect(t *testing.T) {
	var otherAuth, mainAuth atomic.Value
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherAuth.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("{}"))
	}))
	defer other.Close()
	otherURL := strings.Replace(other.URL, "127.0.0.1", "localhost", 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
			return
		}
		mainAuth.Store(r.Header.Get("Authorization"))
		http.Redirect(w, r, otherURL+r.URL.Path, http.StatusFound)
	}))
	defer server.Close()
	host := "http://:SEC-pw@" + strings.TrimPrefix(server.URL, "http://")
	c, err := New(model.ManagerConfig{Host: host, Username: "admin", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.GetCategories(context.Background())
	if got, _ := mainAuth.Load().(string); got != "Basic "+base64.StdEncoding.EncodeToString([]byte(":SEC-pw")) {
		t.Errorf("origin Authorization = %q, want basic from host userinfo", got)
	}
	if got, _ := otherAuth.Load().(string); got != "" {
		t.Errorf("cross-host redirect got Authorization %q, want none", got)
	}
}

func TestEmptyUserBasicKeptOnSameHostRedirect(t *testing.T) {
	var finalAuth atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			http.SetCookie(w, &http.Cookie{Name: "SID", Value: "sid", Path: "/"})
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/categories":
			http.Redirect(w, r, "/final", http.StatusFound)
		default:
			finalAuth.Store(r.Header.Get("Authorization"))
			_, _ = w.Write([]byte("{}"))
		}
	}))
	defer server.Close()
	host := "http://:SEC-pw@" + strings.TrimPrefix(server.URL, "http://")
	c, err := New(model.ManagerConfig{Host: host, Username: "admin", Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.GetCategories(context.Background())
	if got, _ := finalAuth.Load().(string); got != "Basic "+base64.StdEncoding.EncodeToString([]byte(":SEC-pw")) {
		t.Errorf("same-host redirect Authorization = %q, want basic", got)
	}
}

func TestDelete_CallerKeepFilesWins(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("4.6.0"))
	})
	mux.HandleFunc("/api/v2/torrents/delete", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		mu.Lock()
		got["hashes"] = r.FormValue("hashes")
		got["deleteFiles"] = r.FormValue("deleteFiles")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c, err := New(model.ManagerConfig{Host: server.URL, DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"movies"}})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}
	if err = c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "movies", Name: "x", DeleteFiles: false}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got["hashes"] != "h1" || got["deleteFiles"] != "false" {
		t.Fatalf("expected hashes=h1 deleteFiles=false, got %+v", got)
	}
}

func TestDeletesFilesIgnoresEmptyCategory(t *testing.T) {
	c := &Client{config: model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"tv", ""}}}
	if c.DeletesFiles(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: ""}) {
		t.Fatalf("DeletesFiles true for uncategorized torrent")
	}
	if !c.DeletesFiles(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "tv"}) {
		t.Fatalf("DeletesFiles false for listed category")
	}
}

func TestList_NarrowSkipsDroppedProperties(t *testing.T) {
	torrents := []qbt.Torrent{
		{Hash: "a", Name: "name-a", Category: "tv", Tags: "x", AddedOn: 1700000001},
		{Hash: "b", Name: "name-b", Category: "tv", Tags: "x", AddedOn: 1700000002},
		{Hash: "c", Name: "name-c", Category: "tv", Tags: "x", AddedOn: 1700000003},
	}
	var mu sync.Mutex
	var propHashes []string
	server := newStubServer(t, torrents, func(w http.ResponseWriter, r *http.Request) {
		hash := r.URL.Query().Get("hash")
		mu.Lock()
		propHashes = append(propHashes, hash)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(qbt.TorrentProperties{
			Hash:         hash,
			Comment:      "https://example.com/topic/" + hash,
			AdditionDate: 1700000000,
		})
	})
	c, err := New(model.ManagerConfig{Host: server.URL})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	var narrowIn []*model.Entry
	entries, err := c.List(context.Background(), model.FilterOptions{
		Narrow: func(in []*model.Entry) []*model.Entry {
			narrowIn = in
			var kept []*model.Entry
			for _, e := range in {
				if e.Hash == "b" {
					kept = append(kept, e)
				}
			}
			return kept
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 || entries[0].Hash != "b" || entries[0].URL != "https://example.com/topic/b" {
		t.Fatalf("expected only b with its comment, got %+v", entries)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(propHashes) != 1 || propHashes[0] != "b" {
		t.Errorf("expected properties only for b, got %v", propHashes)
	}
	if len(narrowIn) != 3 {
		t.Fatalf("expected 3 entries passed to Narrow, got %d", len(narrowIn))
	}
	for i, e := range narrowIn {
		src := torrents[i]
		if e.Hash != src.Hash || e.Name != src.Name || e.Category != "tv" || e.Tag != "x" {
			t.Errorf("narrow entry %d: %+v", i, e)
		}
		if e.Timestamp == nil || !e.Timestamp.Equal(time.Unix(src.AddedOn, 0)) {
			t.Errorf("narrow entry %d timestamp %v, want %v", i, e.Timestamp, time.Unix(src.AddedOn, 0))
		}
	}
}

func TestMapEntry_ContentPath(t *testing.T) {
	c := &Client{}
	e := c.mapEntry(&qbt.Torrent{Name: "Renamed", SavePath: "/data/tv", ContentPath: "/data/tv/Show S01"}, &qbt.TorrentProperties{})
	if e.ContentPath != "/data/tv/Show S01" {
		t.Fatalf("ContentPath = %q, want the client content path", e.ContentPath)
	}
}
