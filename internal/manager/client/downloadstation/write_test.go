package downloadstation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ripperr/internal/model"
)

func wantParam(t *testing.T, c fakeCall, key, want string) {
	t.Helper()
	if got, ok := c.param(key); !ok || got != want {
		t.Fatalf("%s %s %s = %q (present %v), want %q", c.API, c.Method, key, got, ok, want)
	}
}

func TestAddWithSelection(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	c := newClient(t, f)
	data, hash := buildTorrent(t, "R", topicURL, "a", "b")
	opts := model.ManagerAddFileOptions{
		Hash: hash, Name: "R", SavePath: "downloads/tv", Category: "downloads/other",
		FileSelection: []model.TorrentFile{{Path: "R/a", Wanted: true}, {Path: "R/b", Wanted: false}},
	}
	if err := c.AddFromBuffer(ctx, bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}

	creates := f.callsTo(apiTask2, "create")
	if len(creates) != 1 || creates[0].HTTPMethod != "POST" || creates[0].Query.Get("version") != "2" {
		t.Fatalf("create calls = %+v", creates)
	}
	parts := creates[0].Parts
	fields := map[string]string{}
	for _, p := range parts[:len(parts)-1] {
		fields[p.Name] = string(p.Data)
	}
	last := parts[len(parts)-1]
	if last.Name != "torrent" || last.FileName != hash+".torrent" || !bytes.Equal(last.Data, data) {
		t.Fatalf("last part = %q %q (%d bytes), want torrent %s.torrent", last.Name, last.FileName, len(last.Data), hash)
	}
	if fields["type"] != `"file"` || fields["file"] != `["torrent"]` || fields["create_list"] != "true" {
		t.Fatalf("create fields = %v", fields)
	}

	gets := f.callsTo(apiTaskList, "get")
	if len(gets) != 1 {
		t.Fatalf("Task.List get calls = %d, want 1", len(gets))
	}
	wantParam(t, gets[0], "list_id", `"list_1"`)
	polls := f.callsTo(apiTaskPolling, "download")
	if len(polls) != 1 {
		t.Fatalf("Polling download calls = %d, want 1", len(polls))
	}
	wantParam(t, polls[0], "list_id", `"list_1"`)
	wantParam(t, polls[0], "file_indexes", "[0]")
	wantParam(t, polls[0], "destination", `"downloads/tv"`)
	wantParam(t, polls[0], "create_subfolder", "true")
	if n := len(f.callsTo(apiTask, "pause")); n != 0 {
		t.Fatalf("pause calls = %d, want 0", n)
	}

	if ok, err := c.Exists(ctx, hash); err != nil || !ok {
		t.Fatalf("Exists after add = %v, %v", ok, err)
	}
	if n := f.sourceCalls("dbid_101"); n != 0 {
		t.Fatalf("source downloads of the new task = %d, want 0", n)
	}
	files, err := c.Files(ctx, hash)
	if err != nil || len(files) != 2 || !files[0].Wanted || files[1].Wanted {
		t.Fatalf("Files = %+v, %v, want R/a wanted and R/b skipped", files, err)
	}
}

func TestAddAllFiles(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	f.arrayIDs = true
	c := newClient(t, f)
	data, hash := buildTorrent(t, "R", topicURL, "a", "b")
	if err := c.AddFromBuffer(ctx, bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: hash, Name: "R", SavePath: "downloads/tv"}); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	polls := f.callsTo(apiTaskPolling, "download")
	if len(polls) != 1 {
		t.Fatalf("Polling download calls = %d, want 1", len(polls))
	}
	wantParam(t, polls[0], "list_id", `"list_1"`)
	wantParam(t, polls[0], "file_indexes", "[0,1]")
	if ok, err := c.Exists(ctx, hash); err != nil || !ok || f.sourceCalls("dbid_101") != 0 {
		t.Fatalf("Exists after add = %v, %v, source downloads %d", ok, err, f.sourceCalls("dbid_101"))
	}
}

func TestAddAlreadyPresent(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	data, hash := buildTorrent(t, "R", topicURL, "a")
	if got := f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "R"}, topicURL, "a"); got != hash {
		t.Fatalf("fake hash %s, want %s", got, hash)
	}
	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: hash, Name: "R"})
	if !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("AddFromBuffer = %v, want ErrAlreadyAdded", err)
	}
	if n := len(f.callsTo(apiTask2, "create")); n != 0 {
		t.Fatalf("create calls = %d, want 0", n)
	}
}

func TestAddStoppedPauses(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	c := newClient(t, f)
	data, hash := buildTorrent(t, "R", topicURL, "a")
	if err := c.AddFromBuffer(ctx, bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: hash, Name: "R", Stopped: true}); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	pauses := f.callsTo(apiTask, "pause")
	if len(pauses) != 1 || pauses[0].Query.Get("id") != "dbid_101" || pauses[0].Query.Get("version") != "1" {
		t.Fatalf("pause calls = %+v", pauses)
	}
	var status string
	f.lock(func() { status = f.tasks["dbid_101"].Status })
	if status != statusPaused {
		t.Fatalf("status = %q, want paused", status)
	}

	data2, hash2 := buildTorrent(t, "S", topicURL, "b")
	f.lock(func() { f.actionFault["pause"] = 100 })
	if err := c.AddFromBuffer(ctx, bytes.NewBuffer(data2), model.ManagerAddFileOptions{Hash: hash2, Name: "S", Stopped: true}); err == nil {
		t.Fatalf("AddFromBuffer with failing pause = nil, want error")
	}
}

func TestAddDestinationFallback(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	c := newClient(t, f)
	data, hash := buildTorrent(t, "R", topicURL, "a")
	if err := c.AddFromBuffer(ctx, bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: hash, Name: "R", Category: "downloads/movies"}); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	data2, hash2 := buildTorrent(t, "S", topicURL, "b")
	if err := c.AddFromBuffer(ctx, bytes.NewBuffer(data2), model.ManagerAddFileOptions{Hash: hash2, Name: "S"}); err != nil {
		t.Fatalf("AddFromBuffer without destination: %v", err)
	}
	polls := f.callsTo(apiTaskPolling, "download")
	if len(polls) != 2 {
		t.Fatalf("Polling download calls = %d, want 2", len(polls))
	}
	wantParam(t, polls[0], "destination", `"downloads/movies"`)
	if v, ok := polls[1].param("destination"); ok {
		t.Fatalf("destination without SavePath and Category = %q, want omitted", v)
	}
}

func TestDeleteKeepsFiles(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	c := newClientConfig(t, f, model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"downloads/tv"}})
	hash := f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "R", Destination: "downloads/tv"}, topicURL, "a")
	opts := model.ManagerDeleteOptions{Hash: hash, Category: "downloads/tv", Name: "R", DeleteFiles: true}
	for _, o := range []model.ManagerDeleteOptions{opts, {Hash: hash}, {Category: "downloads/tv", DeleteFiles: true}, {}} {
		if c.DeletesFiles(ctx, o) {
			t.Fatalf("DeletesFiles(%+v) = true", o)
		}
	}
	if err := c.Delete(ctx, opts); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	dels := f.callsTo(apiTask, "delete")
	if len(dels) != 1 || dels[0].Query.Get("id") != "dbid_1" || dels[0].Query.Get("force_complete") != "false" || dels[0].Query.Get("version") != "1" {
		t.Fatalf("delete calls = %+v", dels)
	}
	if ok, err := c.Exists(ctx, hash); err != nil || ok {
		t.Fatalf("Exists after Delete = %v, %v", ok, err)
	}
	if err := c.Delete(ctx, opts); err != nil || len(f.callsTo(apiTask, "delete")) != 1 {
		t.Fatalf("Delete(absent) = %v, delete calls %d", err, len(f.callsTo(apiTask, "delete")))
	}

	f2 := newFake(t)
	c2 := newClientConfig(t, f2, model.ManagerConfig{DeleteFilesEnabled: true, DeleteCategories: []string{"downloads/tv"}})
	hash2 := f2.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "R", Destination: "downloads/tv"}, topicURL, "a")
	n := f2.callCount()
	if err := c2.Delete(ctx, model.ManagerDeleteOptions{Hash: hash2, Category: "downloads/tv", DeleteFiles: true}); err != nil || f2.callCount() != n {
		t.Fatalf("Delete with DeleteEnabled false = %v, calls %d", err, f2.callCount()-n)
	}
}

func TestDiscardResume(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	c := newClient(t, f)
	hash := f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "R", Status: "paused"}, topicURL, "a")
	absent := strings.Repeat("0", 40)

	if err := c.Resume(ctx, hash); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	resumes := f.callsTo(apiTask, "resume")
	if len(resumes) != 1 || resumes[0].Query.Get("id") != "dbid_1" {
		t.Fatalf("resume calls = %+v", resumes)
	}
	if err := c.Resume(ctx, absent); err == nil {
		t.Fatalf("Resume(absent) = nil, want error")
	}

	if err := c.Discard(ctx, hash); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	dels := f.callsTo(apiTask, "delete")
	if len(dels) != 1 || dels[0].Query.Get("id") != "dbid_1" || dels[0].Query.Get("force_complete") != "false" {
		t.Fatalf("delete calls = %+v", dels)
	}
	if err := c.Discard(ctx, absent); err == nil {
		t.Fatalf("Discard(absent) = nil, want error")
	}
}

func TestPurgeRefuses(t *testing.T) {
	f := newFake(t)
	c := newClient(t, f)
	hash := f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "R"}, topicURL, "a")
	n := f.callCount()
	err := c.Purge(context.Background(), hash)
	if !errors.Is(err, model.ErrFilesNotAllowed) || !strings.HasPrefix(err.Error(), "download station keeps files: ") {
		t.Fatalf("Purge = %v, want ErrFilesNotAllowed", err)
	}
	if f.callCount() != n {
		t.Fatalf("Purge made %d calls, want 0", f.callCount()-n)
	}
}

func TestApplySelection(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	c := newClient(t, f)
	hash := f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "R", Files: []fakeFile{
		{Name: "R/a", Priority: "normal"},
		{Name: "R/b", Priority: "skip"},
	}}, topicURL, "a", "b")
	same := model.ManagerAddFileOptions{FileSelection: []model.TorrentFile{{Path: "R/a", Wanted: true}, {Path: "R/b", Wanted: false}}}
	if err := c.ApplySelection(ctx, hash, same); err != nil {
		t.Fatalf("ApplySelection(equal) = %v", err)
	}
	other := model.ManagerAddFileOptions{FileSelection: []model.TorrentFile{{Path: "R/a", Wanted: false}, {Path: "R/b", Wanted: true}}}
	if err := c.ApplySelection(ctx, hash, other); err == nil {
		t.Fatalf("ApplySelection(different) = nil, want error")
	}
	if err := c.ConvergeAdded(ctx, hash, nil, other); err != nil {
		t.Fatalf("ConvergeAdded = %v", err)
	}
}

func TestUnsupported(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	c := newClient(t, f)
	hash := f.addTask(t, "dbid_1", &fakeTask{Type: "bt", Title: "R"}, topicURL, "a")
	if err := c.SetComment(ctx, hash, topicURL); err == nil || errors.Is(err, model.ErrCommentUnsupported) {
		t.Fatalf("SetComment = %v, want an error other than ErrCommentUnsupported", err)
	}
	if _, err := c.ExportTorrent(ctx, hash); !errors.Is(err, model.ErrExportUnsupported) {
		t.Fatalf("ExportTorrent = %v, want ErrExportUnsupported", err)
	}
	if err := c.ForceRedownload(ctx, hash, model.RedownloadOptions{}); !errors.Is(err, ErrRedownloadUnsupported) {
		t.Fatalf("ForceRedownload = %v, want ErrRedownloadUnsupported", err)
	}
	n := f.callCount()
	if err := c.AddFromMagnet(ctx, "magnet:?xt=urn:btih:"+hash, model.ManagerAddFileOptions{Hash: hash}); err == nil || f.callCount() != n {
		t.Fatalf("AddFromMagnet = %v, calls %d, want error and no calls", err, f.callCount()-n)
	}
}

func TestBlackholePairDelegates(t *testing.T) {
	f := newFake(t)
	dir := filepath.Join(t.TempDir(), "blackhole")
	c := newClientConfig(t, f, model.ManagerConfig{BlackholeDir: dir})
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("blackhole dir after New: %v", err)
	}
	data, hash := buildTorrent(t, "R", topicURL, "a")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "R", ReplacesHash: strings.Repeat("1", 40)}
	if err := c.WriteBlackholePair(data, "", opts); err != nil {
		t.Fatalf("WriteBlackholePair: %v", err)
	}
	for _, name := range []string{hash + ".torrent", hash + ".json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("pair file %s: %v", name, err)
		}
	}
	if !c.ReplacePending(hash) {
		t.Fatalf("ReplacePending = false for a pair with ReplacesHash")
	}
	c.RemoveBlackholePair(hash)
	if c.ReplacePending(hash) {
		t.Fatalf("ReplacePending after RemoveBlackholePair = true")
	}
}

func TestAddSeedSurvivesListLag(t *testing.T) {
	old := presenceCheckDelay
	presenceCheckDelay = 10 * time.Millisecond
	t.Cleanup(func() { presenceCheckDelay = old })
	ctx := context.Background()
	f := newFake(t)
	f.lagNewTask = true
	c := newClient(t, f)
	data, hash := buildTorrent(t, "R", topicURL, "a")
	if err := c.AddFromBuffer(ctx, bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: hash, Name: "R"}); err != nil {
		t.Fatalf("AddFromBuffer with a lagging list: %v", err)
	}
	if n := f.sourceCalls("dbid_101"); n != 0 {
		t.Fatalf("source downloads of the new task = %d, want 0", n)
	}
	if ok, err := c.Exists(ctx, hash); err != nil || !ok {
		t.Fatalf("Exists after add = %v, %v", ok, err)
	}
}
