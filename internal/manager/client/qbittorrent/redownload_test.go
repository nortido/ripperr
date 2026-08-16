package qbittorrent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	qbt "github.com/autobrr/go-qbittorrent"

	"trackerr/internal/model"
)

// redownloadStubServer simulates a qbit holding exactly one torrent, matching
// the sequence ForceRedownload drives it through: export, list/details,
// delete, then re-add. filesExistedAtDelete is captured inside the delete
// handler itself so the assertion reflects the real ordering, not a guess
// about timing.
type redownloadStubServer struct {
	mu                   sync.Mutex
	data                 []byte
	torrent              qbt.Torrent
	present              bool
	deleted              bool
	addFail              bool
	dir                  string
	hash                 string
	filesExistedAtDelete bool
	server               *httptest.Server
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
		}
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("/api/v2/torrents/delete", func(w http.ResponseWriter, _ *http.Request) {
		torrentPath, jsonPath := blackholePaths(s.dir, s.hash)
		_, tErr := os.Stat(torrentPath)
		_, jErr := os.Stat(jsonPath)
		s.mu.Lock()
		s.filesExistedAtDelete = tErr == nil && jErr == nil
		s.present = false
		s.deleted = true
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		fail := s.addFail
		s.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		s.present = true
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
	origSettle, origRetry := redownloadSettleDelay, redownloadRetryDelay
	redownloadSettleDelay = 10 * time.Millisecond
	redownloadRetryDelay = 10 * time.Millisecond
	t.Cleanup(func() { redownloadSettleDelay, redownloadRetryDelay = origSettle, origRetry })
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

	if err = c.ForceRedownload(context.Background(), hash); err != nil {
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

	if err = c.ForceRedownload(context.Background(), hash); err == nil {
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

	if err = c.ForceRedownload(context.Background(), hash); err == nil {
		t.Fatal("expected ForceRedownload to fail without a configured blackhole dir")
	}
	if stub.wasDeleted() {
		t.Error("expected qbit delete NOT to have been called when the blackhole write failed")
	}
}

// covers a read-only mount (e.g. blackhole bind-mounted ro by mistake): New
// must still start (the dir already exists, MkdirAll is a no-op on it), and
// ForceRedownload must refuse before touching qbit's delete endpoint.
func TestForceRedownload_ReadOnlyBlackholeDirFailsBeforeDeleting(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission checks are bypassed")
	}
	withFastRedownloadTimings(t)

	data, hash := buildTestTorrent(t)
	dir := t.TempDir()
	stub := newRedownloadStubServer(t, dir, data, hash)

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	c, err := New(model.ManagerConfig{Host: stub.server.URL, DeleteEnabled: true, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v (a pre-existing dir must not fail MkdirAll even read-only)", err)
	}

	if err = c.ForceRedownload(context.Background(), hash); err == nil {
		t.Fatal("expected ForceRedownload to fail when the blackhole dir is read-only")
	}
	if stub.wasDeleted() {
		t.Error("expected qbit delete NOT to have been called when the blackhole write failed")
	}
}
