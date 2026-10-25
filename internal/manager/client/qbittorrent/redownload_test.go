package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	qbt "github.com/autobrr/go-qbittorrent"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/manager/blackhole"
	"ripperr/internal/model"
)

type redownloadStubServer struct {
	mu      sync.Mutex
	data    []byte
	torrent qbt.Torrent
	present bool
	deleted bool
	addFail bool
	dir     string
	hash    string
	// пишется прямо в обработчике удаления, так проверка видит реальный порядок без догадок о таймингах
	filesExistedAtDelete bool
	// содержимое пары в момент удаления: сверяем, что подметальщик добавил бы торрент с тем же комментарием
	torrentAtDelete []byte
	jsonAtDelete    []byte
	lastAdded       []byte
	// торрент один раз отвечает, что он есть (ensurePresentAfterAdd проходит), и тут же пропадает
	// до проверки в ForceRedownload. Так выглядит асинхронное удаление в qbit, влезшее между ними
	unsettleAfterAdd bool
	vanishNext       bool
	files            []qbt.TorrentFile
	filesAfterAdd    []qbt.TorrentFile
	renameCalls      []string
	filesFail        bool
	addCalls         int
	addPaused        string
	prioCalls        []string
	startCalls       int
	server           *httptest.Server
	// после add торрент столько раз не виден в info: ensurePresentAfterAdd сдается, а он уже есть
	hideAfterAdd  int
	stateAfterAdd qbt.TorrentState
}

func newRedownloadStubServer(t *testing.T, dir string, data []byte, hash string) *redownloadStubServer {
	t.Helper()
	s := &redownloadStubServer{
		data:    data,
		dir:     dir,
		hash:    hash,
		present: true,
		torrent: qbt.Torrent{
			Hash: hash, Name: "movie", Category: "movies", Tags: "watch",
			AutoManaged: true, MaxRatio: 2, MaxSeedingTime: 100,
		},
		files: []qbt.TorrentFile{{Name: "test.txt", Priority: 1}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/torrents/files", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.filesFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if s.filesAfterAdd != nil && s.deleted {
			_ = json.NewEncoder(w).Encode(s.filesAfterAdd)
			return
		}
		_ = json.NewEncoder(w).Encode(s.files)
	})
	for kind, p := range map[string]string{"file": "/api/v2/torrents/renameFile", "folder": "/api/v2/torrents/renameFolder"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			s.mu.Lock()
			s.renameCalls = append(s.renameCalls, kind+":"+r.FormValue("oldPath")+">"+r.FormValue("newPath"))
			s.mu.Unlock()
		})
	}
	mux.HandleFunc("/api/v2/torrents/filePrio", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.mu.Lock()
		s.prioCalls = append(s.prioCalls, r.FormValue("id")+"="+r.FormValue("priority"))
		s.mu.Unlock()
	})
	for _, p := range []string{"/api/v2/torrents/start", "/api/v2/torrents/resume"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, _ *http.Request) {
			s.mu.Lock()
			s.startCalls++
			s.mu.Unlock()
		})
	}
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("2.8.3"))
	})
	mux.HandleFunc("/api/v2/torrents/export", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		_, _ = w.Write(s.data)
	})
	mux.HandleFunc("/api/v2/torrents/properties", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(qbt.TorrentProperties{Comment: "https://example.com/topic/1"})
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		var list []qbt.Torrent
		if s.deleted && s.present && s.hideAfterAdd > 0 {
			s.hideAfterAdd--
			_ = json.NewEncoder(w).Encode(list)
			return
		}
		if s.present {
			tor := s.torrent
			if s.deleted && s.stateAfterAdd != "" {
				tor.State = s.stateAfterAdd
			}
			list = append(list, tor)
			if s.vanishNext {
				s.vanishNext = false
				s.present = false
			}
		}
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("/api/v2/torrents/delete", func(w http.ResponseWriter, _ *http.Request) {
		torrentPath, jsonPath := blackholePaths(s.dir, s.hash)
		_, tErr := os.Stat(torrentPath)
		_, jErr := os.Stat(jsonPath)
		s.mu.Lock()
		s.torrentAtDelete, _ = os.ReadFile(torrentPath)
		s.jsonAtDelete, _ = os.ReadFile(jsonPath)
		s.filesExistedAtDelete = tErr == nil && jErr == nil
		s.present = false
		s.deleted = true
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		if r.ParseMultipartForm(1<<20) == nil {
			if file, _, err := r.FormFile("torrents"); err == nil {
				data, _ := io.ReadAll(file)
				_ = file.Close()
				s.mu.Lock()
				s.lastAdded = data
				s.mu.Unlock()
			}
		}
		s.mu.Lock()
		s.addCalls++
		s.addPaused = r.FormValue("paused") + "/" + r.FormValue("stopped")
		fail := s.addFail
		s.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		s.present = true
		if s.unsettleAfterAdd {
			s.vanishNext = true
		}
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

func (s *redownloadStubServer) filesExistedBeforeDelete() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filesExistedAtDelete
}

func (s *redownloadStubServer) wasDeleted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleted
}

func withFastRedownloadTimings(t *testing.T) {
	t.Helper()
	origSettle, origRetry := blackhole.SettleDelay, redownloadRetryDelay
	blackhole.SettleDelay = 10 * time.Millisecond
	redownloadRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { blackhole.SettleDelay, redownloadRetryDelay = origSettle, origRetry })
}

func TestForceRedownload_WritesBlackholeBeforeDeleteAndCleansUpOnSuccess(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildTestTorrent(t)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err != nil {
		t.Fatalf("ForceRedownload: %v", err)
	}

	if !stub.filesExistedBeforeDelete() {
		t.Error("expected blackhole pair to already exist when qbit delete was called")
	}
	if !stub.wasDeleted() {
		t.Error("expected qbit delete to have been called")
	}
	assertPairGone(t, dir, hash)
}

func TestForceRedownload_UnconfirmedPresenceAfterAddLeavesBlackholeFiles(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildTestTorrent(t)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)
	stub.unsettleAfterAdd = true

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err != nil {
		t.Fatalf("expected ForceRedownload to return nil when presence is unconfirmed after add, got %v", err)
	}

	assertPairRemains(t, dir, hash)
}

func TestForceRedownload_AddFailureLeavesBlackholeFiles(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildTestTorrent(t)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)
	stub.addFail = true

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err == nil {
		t.Fatal("expected ForceRedownload to fail when re-add always fails")
	}

	if !stub.filesExistedBeforeDelete() {
		t.Error("expected blackhole pair to already exist when qbit delete was called")
	}
	assertPairRemains(t, dir, hash)
}

func TestForceRedownload_NoBlackholeDirFailsBeforeDeleting(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildTestTorrent(t)
	stub := newRedownloadStubServer(t, "", data, hash)

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err == nil {
		t.Fatal("expected ForceRedownload to fail without a configured blackhole dir")
	}
	if stub.wasDeleted() {
		t.Error("expected qbit delete NOT to have been called when the blackhole write failed")
	}
}

func TestForceRedownload_ReadOnlyBlackholeDirFailsBeforeDeleting(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission checks are bypassed")
	}
	withFastRedownloadTimings(t)

	data, hash := buildTestTorrent(t)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)

	// как blackhole, по ошибке смонтированный ro
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v (a pre-existing dir must not fail MkdirAll even read-only)", err)
	}

	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err == nil {
		t.Fatal("expected ForceRedownload to fail when the blackhole dir is read-only")
	}
	if stub.wasDeleted() {
		t.Error("expected qbit delete NOT to have been called when the blackhole write failed")
	}
}

func TestForceRedownload_CommentOverrideReachesReAddAndBlackholePair(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildTestTorrent(t)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const want = "https://rutracker.org/forum/viewtopic.php?t=1"
	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{Comment: want}); err != nil {
		t.Fatalf("ForceRedownload: %v", err)
	}

	stub.mu.Lock()
	added, pairTorrent, pairJSON := stub.lastAdded, stub.torrentAtDelete, stub.jsonAtDelete
	stub.mu.Unlock()

	assertTorrentComment(t, "re-added torrent", added, hash, want)
	assertTorrentComment(t, "blackhole torrent", pairTorrent, hash, want)
	var meta struct{ Comment string }
	if err = json.Unmarshal(pairJSON, &meta); err != nil {
		t.Fatalf("blackhole json: %v (%q)", err, pairJSON)
	}
	if meta.Comment != want {
		t.Errorf("blackhole json comment = %q, want %q", meta.Comment, want)
	}
}

func TestForceRedownload_NoCommentOverrideKeepsExportedBytes(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildTestTorrent(t)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err != nil {
		t.Fatalf("ForceRedownload: %v", err)
	}

	stub.mu.Lock()
	added, pairJSON := stub.lastAdded, stub.jsonAtDelete
	stub.mu.Unlock()
	if string(added) != string(data) {
		t.Error("re-added torrent bytes differ from the exported ones without a comment override")
	}
	var meta struct{ Comment string }
	if err = json.Unmarshal(pairJSON, &meta); err != nil {
		t.Fatalf("blackhole json: %v (%q)", err, pairJSON)
	}
	if meta.Comment != "https://example.com/topic/1" {
		t.Errorf("blackhole json comment = %q, want the qbit comment", meta.Comment)
	}
}

func assertTorrentComment(t *testing.T, what string, data []byte, hash, want string) {
	t.Helper()
	if len(data) == 0 {
		t.Fatalf("%s: no bytes captured", what)
	}
	got, err := torrentmeta.InfoHash(data)
	if err != nil {
		t.Fatalf("%s: infohash: %v", what, err)
	}
	if got != hash {
		t.Errorf("%s: infohash = %s, want %s", what, got, hash)
	}
	comment, err := torrentmeta.Comment(data)
	if err != nil {
		t.Fatalf("%s: comment: %v", what, err)
	}
	if comment != want {
		t.Errorf("%s: comment = %q, want %q", what, comment, want)
	}
}

func TestForceRedownloadKeepsSelectionAndStopped(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildTestTorrent(t)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)
	stub.torrent.State = qbt.TorrentStatePausedDl
	stub.files = []qbt.TorrentFile{{Name: "a.mkv", Priority: 1}, {Name: "b.mkv", Priority: 0}}

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err != nil {
		t.Fatalf("ForceRedownload: %v", err)
	}

	stub.mu.Lock()
	addPaused, prio, starts, pairJSON := stub.addPaused, stub.prioCalls, stub.startCalls, stub.jsonAtDelete
	stub.mu.Unlock()
	if !strings.Contains(addPaused, "true") {
		t.Errorf("re-add paused/stopped = %q, want the torrent added stopped", addPaused)
	}
	if len(prio) != 1 || prio[0] != "1=0" {
		t.Errorf("file priority calls = %v, want [1=0]", prio)
	}
	if starts != 0 {
		t.Errorf("start calls = %d, want 0 for a stopped torrent", starts)
	}
	var meta model.ManagerAddFileOptions
	if err = json.Unmarshal(pairJSON, &meta); err != nil {
		t.Fatalf("blackhole json: %v (%q)", err, pairJSON)
	}
	if !meta.Stopped {
		t.Error("blackhole pair lost the stopped state")
	}
	if len(meta.FileSelection) != 2 || !meta.FileSelection[0].Wanted || meta.FileSelection[1].Wanted {
		t.Errorf("blackhole pair selection = %+v, want a wanted, b unwanted", meta.FileSelection)
	}
}

func TestForceRedownloadFilesErrorChangesNothing(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildTestTorrent(t)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)
	stub.filesFail = true

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{})
	if err == nil || !strings.Contains(err.Error(), "read file selection") {
		t.Fatalf("err = %v, want a file selection read error", err)
	}
	if stub.wasDeleted() {
		t.Error("torrent deleted although its file selection could not be read")
	}
	assertPairGone(t, dir, hash)
}

func TestForceRedownloadRefusesV2Torrent(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildHybridTestTorrent(t)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{})
	if err == nil || !strings.Contains(err.Error(), "v2 metadata") {
		t.Fatalf("err = %v, want a v2 metadata refusal", err)
	}
	// удаление с файлами без повторного добавления потеряло бы раздачу
	if stub.wasDeleted() {
		t.Error("v2 torrent deleted although it can never be re-added")
	}
	assertPairGone(t, dir, hash)
}

func TestForceRedownloadKeepsV1TorrentWithOddInfo(t *testing.T) {
	withFastRedownloadTimings(t)

	data := oddInfoTorrent()
	hash, err := torrentmeta.InfoHash(data)
	if err != nil {
		t.Fatalf("InfoHash: %v", err)
	}
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err != nil {
		t.Fatalf("ForceRedownload: %v", err)
	}
	stub.mu.Lock()
	adds := stub.addCalls
	stub.mu.Unlock()
	if adds != 1 {
		t.Errorf("add requests = %d, want 1", adds)
	}
}

func buildMultiFileTestTorrent(t *testing.T, root string, names ...string) ([]byte, string) {
	t.Helper()
	info := metainfo.Info{PieceLength: 16384, Pieces: make([]byte, 20), Name: root}
	for _, n := range names {
		info.Files = append(info.Files, metainfo.FileInfo{Length: 1, Path: []string{n}})
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes, Announce: "http://tracker.example/announce"}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes(), mi.HashInfoBytes().HexString()
}

func TestForceRedownloadRestoresRenamedFile(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildMultiFileTestTorrent(t, "Show", "e1.mkv", "e2.mkv")
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)
	stub.files = []qbt.TorrentFile{{Name: "Show/e1.mkv", Priority: 1}, {Index: 1, Name: "Show/Episode 2.mkv", Priority: 1}}
	stub.filesAfterAdd = []qbt.TorrentFile{{Name: "Show/e1.mkv", Priority: 1}, {Index: 1, Name: "Show/e2.mkv", Priority: 1}}

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err != nil {
		t.Fatalf("ForceRedownload: %v", err)
	}

	stub.mu.Lock()
	addPaused, renames, starts, pairJSON := stub.addPaused, stub.renameCalls, stub.startCalls, stub.jsonAtDelete
	stub.mu.Unlock()
	var meta model.ManagerAddFileOptions
	if err = json.Unmarshal(pairJSON, &meta); err != nil {
		t.Fatalf("blackhole json: %v (%q)", err, pairJSON)
	}
	if want := []string{"Show/e1.mkv", "Show/Episode 2.mkv"}; !slices.Equal(meta.FilePaths, want) {
		t.Errorf("blackhole pair FilePaths = %q, want %q", meta.FilePaths, want)
	}
	if want := []string{"file:Show/e2.mkv>Show/Episode 2.mkv"}; !slices.Equal(renames, want) {
		t.Errorf("rename calls = %q, want %q", renames, want)
	}
	if strings.Contains(addPaused, "true") {
		t.Errorf("re-add paused/stopped = %q, want the torrent added started", addPaused)
	}
	if starts != 0 {
		t.Errorf("start calls = %d, want 0 for a torrent added started", starts)
	}
	assertPairGone(t, dir, hash)
}

func TestForceRedownloadRenamedSelectionStaysPartial(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildMultiFileTestTorrent(t, "Show", "e1.mkv", "e2.mkv")
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)
	stub.torrent.State = qbt.TorrentStatePausedDl
	stub.files = []qbt.TorrentFile{{Name: "Show/Episode 1.mkv", Priority: 1}, {Index: 1, Name: "Show/e2.mkv", Priority: 0}}
	stub.filesAfterAdd = []qbt.TorrentFile{{Name: "Show/e1.mkv", Priority: 1}, {Index: 1, Name: "Show/e2.mkv", Priority: 1}}

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err != nil {
		t.Fatalf("ForceRedownload: %v", err)
	}

	stub.mu.Lock()
	prio, renames, starts := stub.prioCalls, stub.renameCalls, stub.startCalls
	stub.mu.Unlock()
	if want := []string{"1=0"}; !slices.Equal(prio, want) {
		t.Errorf("file priority calls = %v, want %v", prio, want)
	}
	if want := []string{"file:Show/e1.mkv>Show/Episode 1.mkv"}; !slices.Equal(renames, want) {
		t.Errorf("rename calls = %q, want %q", renames, want)
	}
	if starts != 0 {
		t.Errorf("start calls = %d, want 0 for a stopped torrent", starts)
	}
}

func TestForceRedownloadWithoutRenamesAddsStarted(t *testing.T) {
	withFastRedownloadTimings(t)

	data, hash := buildTestTorrent(t)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err = c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err != nil {
		t.Fatalf("ForceRedownload: %v", err)
	}

	stub.mu.Lock()
	addPaused, renames, pairJSON := stub.addPaused, stub.renameCalls, stub.jsonAtDelete
	stub.mu.Unlock()
	var meta model.ManagerAddFileOptions
	if err = json.Unmarshal(pairJSON, &meta); err != nil {
		t.Fatalf("blackhole json: %v (%q)", err, pairJSON)
	}
	if meta.FilePaths != nil {
		t.Errorf("blackhole pair FilePaths = %q, want nil", meta.FilePaths)
	}
	if strings.Contains(addPaused, "true") {
		t.Errorf("re-add paused/stopped = %q, want the torrent added started", addPaused)
	}
	if len(renames) != 0 {
		t.Errorf("rename calls = %q, want none", renames)
	}
}

func newLateAddStub(t *testing.T, root string, names ...string) (*Client, *redownloadStubServer, string) {
	t.Helper()
	withFastRedownloadTimings(t)
	orig := presencePoll
	presencePoll = time.Millisecond
	t.Cleanup(func() { presencePoll = orig })
	data, hash := buildMultiFileTestTorrent(t, root, names...)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)
	stub.torrent.State = qbt.TorrentStateDownloading
	stub.hideAfterAdd = 5
	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, stub, hash
}

func TestForceRedownloadLateAddAppliesSelection(t *testing.T) {
	c, stub, hash := newLateAddStub(t, "Show", "a.mkv", "b.mkv")
	stub.stateAfterAdd = qbt.TorrentStateStoppedDl
	stub.files = []qbt.TorrentFile{{Name: "Show/a.mkv", Priority: 1}, {Index: 1, Name: "Show/b.mkv", Priority: 0}}
	stub.filesAfterAdd = []qbt.TorrentFile{{Name: "Show/a.mkv", Priority: 1}, {Index: 1, Name: "Show/b.mkv", Priority: 1}}

	if err := c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err != nil {
		t.Fatalf("ForceRedownload: %v", err)
	}
	stub.mu.Lock()
	adds, prio, starts, deleted := stub.addCalls, stub.prioCalls, stub.startCalls, stub.deleted
	stub.mu.Unlock()
	if adds != 1 {
		t.Fatalf("add requests = %d, want 1 (the second attempt must meet the late torrent)", adds)
	}
	if want := []string{"1=0"}; !slices.Equal(prio, want) {
		t.Errorf("file priority calls = %v, want %v", prio, want)
	}
	if starts != 1 {
		t.Errorf("start calls = %d, want 1", starts)
	}
	if !deleted {
		t.Error("expected the redownload delete")
	}
}

func TestForceRedownloadLateAddRestoresNames(t *testing.T) {
	c, stub, hash := newLateAddStub(t, "Show", "e1.mkv", "e2.mkv")
	stub.files = []qbt.TorrentFile{{Name: "Show/e1.mkv", Priority: 1}, {Index: 1, Name: "Show/Episode 2.mkv", Priority: 1}}
	stub.filesAfterAdd = []qbt.TorrentFile{{Name: "Show/e1.mkv", Priority: 1}, {Index: 1, Name: "Show/e2.mkv", Priority: 1}}

	if err := c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err != nil {
		t.Fatalf("ForceRedownload: %v", err)
	}
	stub.mu.Lock()
	adds, renames, prio, starts := stub.addCalls, stub.renameCalls, stub.prioCalls, stub.startCalls
	stub.mu.Unlock()
	if adds != 1 {
		t.Fatalf("add requests = %d, want 1", adds)
	}
	if want := []string{"file:Show/e2.mkv>Show/Episode 2.mkv"}; !slices.Equal(renames, want) {
		t.Errorf("rename calls = %q, want %q", renames, want)
	}
	if len(prio) != 0 || starts != 0 {
		t.Errorf("file priority calls = %v, start calls = %d, want none for a torrent added started", prio, starts)
	}
}

func TestForceRedownloadLateAddConvergeFailureKeepsPair(t *testing.T) {
	c, stub, hash := newLateAddStub(t, "Show", "a.mkv", "b.mkv")
	stub.stateAfterAdd = qbt.TorrentStateCheckingResumeData
	stub.files = []qbt.TorrentFile{{Name: "Show/a.mkv", Priority: 1}, {Index: 1, Name: "Show/b.mkv", Priority: 0}}
	stub.filesAfterAdd = []qbt.TorrentFile{{Name: "Show/a.mkv", Priority: 1}, {Index: 1, Name: "Show/b.mkv", Priority: 1}}

	if err := c.ForceRedownload(context.Background(), hash, model.RedownloadOptions{}); err != nil {
		t.Fatalf("ForceRedownload: %v, want nil: the torrent is back, the sweeper finishes it", err)
	}
	stub.mu.Lock()
	adds, prio, starts := stub.addCalls, stub.prioCalls, stub.startCalls
	stub.mu.Unlock()
	if adds != 1 || len(prio) != 0 || starts != 0 {
		t.Errorf("add requests = %d, file priority calls = %v, start calls = %d, want 1, none, 0", adds, prio, starts)
	}
	assertPairRemains(t, stub.dir, hash)
}
