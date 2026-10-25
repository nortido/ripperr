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
	// 0 значит 200
	renameStatus int
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
		"add":          {"stopped", "paused"},
		"filePrio":     {"hash", "id", "priority"},
		"start":        {"hashes"},
		"delete":       {"hashes", "deleteFiles"},
		"renameFile":   {"oldPath", "newPath"},
		"renameFolder": {"oldPath", "newPath"},
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
		case "renameFile", "renameFolder":
			if rec.renameStatus != 0 {
				w.WriteHeader(rec.renameStatus)
				return
			}
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

func countOf(log []string, endpoint string) int {
	n := 0
	for _, e := range log {
		if e == endpoint {
			n++
		}
	}
	return n
}

func TestAddFromBuffer_FilePathsRenamedFileThenSelection(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, filesOf("R/e1.mkv", "R/e2.mkv"), http.StatusOK)
	opts := model.ManagerAddFileOptions{
		Name:          "n",
		FileSelection: []model.TorrentFile{{Path: "R/Episode 1.mkv", Wanted: true}, {Path: "R/e2.mkv", Wanted: false}},
		FilePaths:     []string{"R/Episode 1.mkv", "R/e2.mkv"},
	}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if got := rec.form("add")["stopped"]; got != "true" {
		t.Fatalf("add stopped = %q, want true", got)
	}
	log := rec.log()
	if n := countOf(log, "renameFile"); n != 1 {
		t.Fatalf("renameFile calls = %d, want 1, log %v", n, log)
	}
	if rf := rec.form("renameFile"); rf["oldPath"] != "R/e1.mkv" || rf["newPath"] != "R/Episode 1.mkv" {
		t.Fatalf("renameFile form = %v, want R/e1.mkv -> R/Episode 1.mkv", rf)
	}
	prio := rec.form("filePrio")
	if prio["id"] != "1" || prio["priority"] != "0" {
		t.Fatalf("filePrio form = %v, want id=1 priority=0, log %v", prio, log)
	}
	ir, ip, is := slices.Index(log, "renameFile"), slices.Index(log, "filePrio"), slices.Index(log, "start")
	if ir < 0 || ip < ir || is < ip {
		t.Fatalf("want renameFile < filePrio < start, log %v", log)
	}
	if slices.Contains(log, "delete") {
		t.Fatalf("unexpected delete, log %v", log)
	}
}

func TestAddFromBuffer_FilePathsRootFolderRename(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, filesOf("R1/a", "R1/b"), http.StatusOK)
	opts := model.ManagerAddFileOptions{Name: "n", FilePaths: []string{"R2/a", "R2/b"}}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if got := rec.form("add")["stopped"]; got != "false" {
		t.Fatalf("add stopped = %q, want false", got)
	}
	log := rec.log()
	if n := countOf(log, "renameFolder"); n != 1 {
		t.Fatalf("renameFolder calls = %d, want 1, log %v", n, log)
	}
	if rf := rec.form("renameFolder"); rf["oldPath"] != "R1" || rf["newPath"] != "R2" {
		t.Fatalf("renameFolder form = %v, want R1 -> R2", rf)
	}
	if slices.Contains(log, "renameFile") {
		t.Fatalf("unexpected renameFile, log %v", log)
	}
}

func TestAddFromBuffer_FilePathsRenameConflictAddsStarted(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, filesOf("R/e1.mkv", "R/e2.mkv"), http.StatusOK)
	rec.mu.Lock()
	rec.renameStatus = http.StatusConflict
	rec.mu.Unlock()
	opts := model.ManagerAddFileOptions{Name: "n", FilePaths: []string{"R/Episode 1.mkv", "R/e2.mkv"}}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if got := rec.form("add")["stopped"]; got != "false" {
		t.Fatalf("add stopped = %q, want false", got)
	}
	log := rec.log()
	if n := countOf(log, "renameFile"); n != 1 {
		t.Fatalf("renameFile calls = %d, want 1, log %v", n, log)
	}
	if slices.Contains(log, "delete") {
		t.Fatalf("unexpected delete, log %v", log)
	}
}

func TestAddFromBuffer_FilePathsCountMismatchSkipsRenames(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, filesOf("R/a", "R/b"), http.StatusOK)
	opts := model.ManagerAddFileOptions{Name: "n", FilePaths: []string{"R/a", "R/b", "R/c"}}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	log := rec.log()
	if slices.Contains(log, "renameFile") || slices.Contains(log, "renameFolder") {
		t.Fatalf("unexpected rename, log %v", log)
	}
	if got := rec.form("add")["stopped"]; got != "false" {
		t.Fatalf("add stopped = %q, want false", got)
	}
}

func TestAddFromBuffer_FilePathsStoppedNotStarted(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, filesOf("R1/a", "R1/b"), http.StatusOK)
	opts := model.ManagerAddFileOptions{Name: "n", Stopped: true, FilePaths: []string{"R2/a", "R2/b"}}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if got := rec.form("add")["stopped"]; got != "true" {
		t.Fatalf("add stopped = %q, want true", got)
	}
	log := rec.log()
	if n := countOf(log, "renameFolder"); n != 1 {
		t.Fatalf("renameFolder calls = %d, want 1, log %v", n, log)
	}
	if slices.Contains(log, "start") || slices.Contains(log, "resume") {
		t.Fatalf("stopped add must not start, log %v", log)
	}
}

func TestAddFromBuffer_FilePathsOnlyAddsStarted(t *testing.T) {
	data, hash := buildTestTorrent(t)
	c, rec := newSelectionServer(t, hash, filesOf("R1/a", "R1/b"), http.StatusOK)
	opts := model.ManagerAddFileOptions{Name: "n", FilePaths: []string{"R2/a", "R2/b"}}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if f := rec.form("add"); f["stopped"] != "false" || f["paused"] != "false" {
		t.Fatalf("add form = %v, want stopped=false paused=false", f)
	}
	log := rec.log()
	if n := countOf(log, "renameFolder"); n != 1 {
		t.Fatalf("renameFolder calls = %d, want 1, log %v", n, log)
	}
	if slices.Contains(log, "start") || slices.Contains(log, "resume") {
		t.Fatalf("started add needs no start call, log %v", log)
	}
}
