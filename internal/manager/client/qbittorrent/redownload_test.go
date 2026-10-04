package qbittorrent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

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
	server           *httptest.Server
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
	}
	mux := http.NewServeMux()
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
		if s.present {
			list = append(list, s.torrent)
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
