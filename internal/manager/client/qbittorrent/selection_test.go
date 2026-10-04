package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	qbt "github.com/autobrr/go-qbittorrent"

	"ripperr/internal/model"
)

type selectionRecorder struct {
	mu        sync.Mutex
	endpoints []string
	forms     map[string]map[string]string
}

func (r *selectionRecorder) log() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.endpoints...)
}

func (r *selectionRecorder) form(endpoint string) map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.forms[endpoint]
}

func newSelectionServer(t *testing.T, hash string, files qbt.TorrentFiles, prioStatus int) (*Client, *selectionRecorder) {
	t.Helper()
	rec := &selectionRecorder{forms: map[string]map[string]string{}}
	added := false
	keys := map[string][]string{
		"add":      {"stopped", "paused"},
		"filePrio": {"hash", "id", "priority"},
		"start":    {"hashes"},
		"delete":   {"hashes", "deleteFiles"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("2.11.2"))
	})
	mux.HandleFunc("/api/v2/torrents/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/api/v2/torrents/")
		if name == "add" {
			_ = r.ParseMultipartForm(32 << 20)
		}
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.endpoints = append(rec.endpoints, name)
		if ks, ok := keys[name]; ok {
			f := map[string]string{}
			for _, k := range ks {
				f[k] = r.FormValue(k)
			}
			rec.forms[name] = f
		}
		switch name {
		case "info":
			if added {
				_ = json.NewEncoder(w).Encode([]qbt.Torrent{{Hash: hash, Name: "new"}})
				return
			}
			_ = json.NewEncoder(w).Encode([]qbt.Torrent{})
			return
		case "add":
			added = true
		case "files":
			_ = json.NewEncoder(w).Encode(files)
			return
		case "filePrio":
			w.WriteHeader(prioStatus)
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
	return c, rec
}

func filesOf(names ...string) qbt.TorrentFiles {
	out := make(qbt.TorrentFiles, len(names))
	for i, n := range names {
		out[i] = qbt.TorrentFile{Index: i, Name: n, Priority: 1}
	}
	return out
}

var oldSelection = []model.TorrentFile{{Path: "R1/a", Wanted: true}, {Path: "R1/b", Wanted: false}}

func TestFiles_PriorityZeroIsUnwanted(t *testing.T) {
	files := qbt.TorrentFiles{
		{Index: 0, Name: "R/a", Priority: 1},
		{Index: 1, Name: "R/b", Priority: 0},
		{Index: 2, Name: "R/c", Priority: 6},
	}
	c, _ := newSelectionServer(t, "h1", files, http.StatusOK)
	got, err := c.Files(context.Background(), "h1")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []model.TorrentFile{{Path: "R/a", Wanted: true}, {Path: "R/b", Wanted: false}, {Path: "R/c", Wanted: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Files = %v, want %v", got, want)
	}
}

func TestAddFromBuffer_SelectionAddsStoppedSetsPriorityThenStarts(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, filesOf("R2/a", "R2/b", "R2/c"), http.StatusOK)
	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "n", FileSelection: oldSelection})
	if err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if got := rec.form("add")["stopped"]; got != "true" {
		t.Fatalf("add stopped = %q, want true", got)
	}
	prio := rec.form("filePrio")
	if prio["hash"] != hash || prio["id"] != "1|2" || prio["priority"] != "0" {
		t.Fatalf("filePrio form = %v, want hash=%s id=1|2 priority=0", prio, hash)
	}
	if got := rec.form("start")["hashes"]; got != hash {
		t.Fatalf("start hashes = %q, want %s", got, hash)
	}
	log := rec.log()
	ip, is := slices.Index(log, "filePrio"), slices.Index(log, "start")
	if ip < 0 || is < ip {
		t.Fatalf("start must follow filePrio, log %v", log)
	}
	if slices.Contains(log, "delete") {
		t.Fatalf("unexpected delete, log %v", log)
	}
}

func TestAddFromBuffer_SelectionKeepsStoppedWhenAsked(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, filesOf("R2/a", "R2/b", "R2/c"), http.StatusOK)
	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "n", Stopped: true, FileSelection: oldSelection})
	if err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if got := rec.form("add")["stopped"]; got != "true" {
		t.Fatalf("add stopped = %q, want true", got)
	}
	if got := rec.form("filePrio")["id"]; got != "1|2" {
		t.Fatalf("filePrio id = %q, want 1|2", got)
	}
	if log := rec.log(); slices.Contains(log, "start") || slices.Contains(log, "resume") {
		t.Fatalf("stopped add must not start, log %v", log)
	}
}

func TestAddFromBuffer_SelectionNothingUnwantedStillStarts(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, filesOf("R2/a"), http.StatusOK)
	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "n", FileSelection: oldSelection})
	if err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	log := rec.log()
	if slices.Contains(log, "filePrio") {
		t.Fatalf("unexpected filePrio, log %v", log)
	}
	if got := rec.form("start")["hashes"]; got != hash {
		t.Fatalf("start hashes = %q, want %s, log %v", got, hash, log)
	}
}

func TestAddFromBuffer_SelectionFailureDiscardsWithoutFiles(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, filesOf("R2/a", "R2/b", "R2/c"), http.StatusConflict)
	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "n", FileSelection: oldSelection})
	if err == nil {
		t.Fatal("expected error when filePrio fails")
	}
	del := rec.form("delete")
	if del["hashes"] != hash || del["deleteFiles"] != "false" {
		t.Fatalf("delete form = %v, want hashes=%s deleteFiles=false", del, hash)
	}
	if log := rec.log(); slices.Contains(log, "start") {
		t.Fatalf("failed apply must not start, log %v", log)
	}
}

func TestAddFromBuffer_NoSelectionNoExtraCalls(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, filesOf("R2/a"), http.StatusOK)
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "n"}); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if got := rec.form("add")["stopped"]; got != "false" {
		t.Fatalf("add stopped = %q, want false", got)
	}
	log := rec.log()
	for _, e := range []string{"files", "filePrio", "start", "resume", "delete"} {
		if slices.Contains(log, e) {
			t.Fatalf("unexpected %s, log %v", e, log)
		}
	}
}

func TestAddFromBuffer_SelectionEmptyFileListDiscards(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, qbt.TorrentFiles{}, http.StatusOK)
	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Name: "n", FileSelection: oldSelection})
	if err == nil {
		t.Fatal("expected error when the new file list is empty")
	}
	del := rec.form("delete")
	if del["hashes"] != hash || del["deleteFiles"] != "false" {
		t.Fatalf("delete form = %v, want hashes=%s deleteFiles=false", del, hash)
	}
	if log := rec.log(); slices.Contains(log, "start") {
		t.Fatalf("empty file list must not start, log %v", log)
	}
}
