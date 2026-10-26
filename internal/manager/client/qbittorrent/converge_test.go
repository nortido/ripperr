package qbittorrent

import (
	"context"
	"net/http"
	"slices"
	"testing"

	qbt "github.com/autobrr/go-qbittorrent"

	"ripperr/internal/model"
)

var convergeSelection = []model.TorrentFile{{Path: "Show/Episode 1.mkv", Wanted: true}, {Path: "Show/e2.mkv", Wanted: false}}

var plainSelection = []model.TorrentFile{{Path: "Show/e1.mkv", Wanted: true}, {Path: "Show/e2.mkv", Wanted: false}}

func newConvergeServer(t *testing.T, state qbt.TorrentState, files qbt.TorrentFiles, prioStatus int) (*Client, *selectionRecorder, []byte, string) {
	t.Helper()
	data, hash := buildMultiFileTestTorrent(t, "Show", "e1.mkv", "e2.mkv")
	c, rec := newSelectionServer(t, hash, files, prioStatus)
	rec.mu.Lock()
	rec.state = state
	rec.mu.Unlock()
	return c, rec, data, hash
}

func TestConvergeAddedStoppedUntouchedAppliesAll(t *testing.T) {
	c, rec, data, hash := newConvergeServer(t, qbt.TorrentStateStoppedDl, filesOf("Show/e1.mkv", "Show/e2.mkv"), http.StatusOK)
	opts := model.ManagerAddFileOptions{Name: "n", FileSelection: convergeSelection, FilePaths: []string{"Show/Episode 1.mkv", "Show/e2.mkv"}}
	if err := c.ConvergeAdded(context.Background(), hash, data, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	log := rec.log()
	if rf := rec.form("renameFile"); rf["oldPath"] != "Show/e1.mkv" || rf["newPath"] != "Show/Episode 1.mkv" {
		t.Fatalf("renameFile form = %v, log %v", rf, log)
	}
	if prio := rec.form("filePrio"); prio["id"] != "1" || prio["priority"] != "0" {
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

func TestConvergeAddedKeepsStoppedWhenAsked(t *testing.T) {
	c, rec, data, hash := newConvergeServer(t, qbt.TorrentStatePausedDl, filesOf("Show/e1.mkv", "Show/e2.mkv"), http.StatusOK)
	opts := model.ManagerAddFileOptions{Name: "n", Stopped: true, FileSelection: plainSelection}
	if err := c.ConvergeAdded(context.Background(), hash, data, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	log := rec.log()
	if got := rec.form("filePrio")["id"]; got != "1" {
		t.Fatalf("filePrio id = %q, want 1, log %v", got, log)
	}
	if slices.Contains(log, "start") || slices.Contains(log, "delete") {
		t.Fatalf("unexpected start or delete, log %v", log)
	}
}

func TestConvergeAddedRunningLeftAlone(t *testing.T) {
	c, rec, data, hash := newConvergeServer(t, qbt.TorrentStateDownloading, filesOf("Show/e1.mkv", "Show/e2.mkv"), http.StatusOK)
	opts := model.ManagerAddFileOptions{Name: "n", FileSelection: convergeSelection, FilePaths: []string{"Show/Episode 1.mkv", "Show/e2.mkv"}}
	if err := c.ConvergeAdded(context.Background(), hash, data, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if log := rec.log(); !slices.Equal(log, []string{"info"}) {
		t.Fatalf("log = %v, want only info", log)
	}
}

func convergeOurSelectionStarts(t *testing.T, files qbt.TorrentFiles, opts model.ManagerAddFileOptions) {
	t.Helper()
	c, rec, data, hash := newConvergeServer(t, qbt.TorrentStateStoppedDl, files, http.StatusOK)
	if err := c.ConvergeAdded(context.Background(), hash, data, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if log := rec.log(); !slices.Equal(log, []string{"info", "files", "start"}) {
		t.Fatalf("log = %v, want info, files and start only", log)
	}
	if got := rec.form("start")["hashes"]; got != hash {
		t.Fatalf("start hashes = %q, want %s", got, hash)
	}
}

func TestConvergeAddedStoppedWithOurSelectionStarts(t *testing.T) {
	files := qbt.TorrentFiles{{Index: 0, Name: "Show/e1.mkv", Priority: 1}, {Index: 1, Name: "Show/e2.mkv", Priority: 0}}
	t.Run("file paths", func(t *testing.T) {
		convergeOurSelectionStarts(t, files, model.ManagerAddFileOptions{Name: "n", FileSelection: convergeSelection, FilePaths: []string{"Show/Episode 1.mkv", "Show/e2.mkv"}})
	})
	t.Run("plain", func(t *testing.T) {
		convergeOurSelectionStarts(t, files, model.ManagerAddFileOptions{Name: "n", FileSelection: plainSelection})
	})
}

func TestConvergeAddedAllUnwantedStoppedStarts(t *testing.T) {
	files := qbt.TorrentFiles{{Index: 0, Name: "Show/e1.mkv", Priority: 0}, {Index: 1, Name: "Show/e2.mkv", Priority: 0}}
	selection := []model.TorrentFile{{Path: "Show/e1.mkv", Wanted: false}, {Path: "Show/e2.mkv", Wanted: false}}
	convergeOurSelectionStarts(t, files, model.ManagerAddFileOptions{Name: "n", FileSelection: selection})
}

func TestConvergeAddedStartFailureKeepsTorrent(t *testing.T) {
	files := qbt.TorrentFiles{{Index: 0, Name: "Show/e1.mkv", Priority: 1}, {Index: 1, Name: "Show/e2.mkv", Priority: 0}}
	c, rec, data, hash := newConvergeServer(t, qbt.TorrentStateStoppedDl, files, http.StatusOK)
	rec.mu.Lock()
	rec.startStatus = http.StatusConflict
	rec.mu.Unlock()
	opts := model.ManagerAddFileOptions{Name: "n", FileSelection: plainSelection}
	if err := c.ConvergeAdded(context.Background(), hash, data, opts); err == nil {
		t.Fatal("expected an error when start fails")
	}
	log := rec.log()
	if n := countOf(log, "start"); n != 1 {
		t.Fatalf("start calls = %d, want 1, log %v", n, log)
	}
	if slices.Contains(log, "filePrio") || slices.Contains(log, "delete") {
		t.Fatalf("unexpected filePrio or delete, log %v", log)
	}
}

func TestConvergeAddedStoppedWithOtherSelectionLeftAlone(t *testing.T) {
	files := qbt.TorrentFiles{{Index: 0, Name: "Show/e1.mkv", Priority: 0}, {Index: 1, Name: "Show/e2.mkv", Priority: 1}}
	c, rec, data, hash := newConvergeServer(t, qbt.TorrentStateStoppedDl, files, http.StatusOK)
	opts := model.ManagerAddFileOptions{Name: "n", FileSelection: convergeSelection, FilePaths: []string{"Show/Episode 1.mkv", "Show/e2.mkv"}}
	if err := c.ConvergeAdded(context.Background(), hash, data, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if log := rec.log(); !slices.Equal(log, []string{"info", "files"}) {
		t.Fatalf("log = %v, want info and files only", log)
	}
}

func TestConvergeAddedOurSelectionStoppedWhenAsked(t *testing.T) {
	files := qbt.TorrentFiles{{Index: 0, Name: "Show/e1.mkv", Priority: 1}, {Index: 1, Name: "Show/e2.mkv", Priority: 0}}
	c, rec, data, hash := newConvergeServer(t, qbt.TorrentStateStoppedDl, files, http.StatusOK)
	opts := model.ManagerAddFileOptions{Name: "n", Stopped: true, FileSelection: convergeSelection, FilePaths: []string{"Show/Episode 1.mkv", "Show/e2.mkv"}}
	if err := c.ConvergeAdded(context.Background(), hash, data, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if log := rec.log(); !slices.Equal(log, []string{"info", "files"}) {
		t.Fatalf("log = %v, want info and files only", log)
	}
}

func TestConvergeAddedCheckingIsError(t *testing.T) {
	c, rec, data, hash := newConvergeServer(t, qbt.TorrentStateCheckingDl, filesOf("Show/e1.mkv", "Show/e2.mkv"), http.StatusOK)
	opts := model.ManagerAddFileOptions{Name: "n", FileSelection: convergeSelection}
	if err := c.ConvergeAdded(context.Background(), hash, data, opts); err == nil {
		t.Fatal("expected a not settled error while checking")
	}
	if log := rec.log(); !slices.Equal(log, []string{"info"}) {
		t.Fatalf("log = %v, want only info", log)
	}
}

func TestConvergeAddedNamesChangedAfterAddSkipsRenames(t *testing.T) {
	c, rec, data, hash := newConvergeServer(t, qbt.TorrentStateDownloading, filesOf("Show/mine.mkv", "Show/e2.mkv"), http.StatusOK)
	opts := model.ManagerAddFileOptions{Name: "n", FilePaths: []string{"Show/Episode 1.mkv", "Show/e2.mkv"}}
	if err := c.ConvergeAdded(context.Background(), hash, data, opts); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	log := rec.log()
	if slices.Contains(log, "renameFile") || slices.Contains(log, "renameFolder") {
		t.Fatalf("unexpected rename, log %v", log)
	}
}

func TestConvergeAddedNothingSavedNoRequests(t *testing.T) {
	c, rec, data, hash := newConvergeServer(t, qbt.TorrentStateStoppedDl, filesOf("Show/e1.mkv", "Show/e2.mkv"), http.StatusOK)
	if err := c.ConvergeAdded(context.Background(), hash, data, model.ManagerAddFileOptions{Name: "n"}); err != nil {
		t.Fatalf("ConvergeAdded: %v", err)
	}
	if log := rec.log(); len(log) != 0 {
		t.Fatalf("log = %v, want no requests", log)
	}
}

func TestConvergeAddedSelectionFailureKeepsTorrent(t *testing.T) {
	c, rec, data, hash := newConvergeServer(t, qbt.TorrentStateStoppedDl, filesOf("Show/e1.mkv", "Show/e2.mkv"), http.StatusConflict)
	opts := model.ManagerAddFileOptions{Name: "n", FileSelection: plainSelection}
	if err := c.ConvergeAdded(context.Background(), hash, data, opts); err == nil {
		t.Fatal("expected an error when filePrio fails")
	}
	log := rec.log()
	if n := countOf(log, "delete"); n != 0 {
		t.Fatalf("delete calls = %d, want 0, log %v", n, log)
	}
	if slices.Contains(log, "start") {
		t.Fatalf("failed apply must not start, log %v", log)
	}
}
