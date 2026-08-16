package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	qbt "github.com/autobrr/go-qbittorrent"

	"trackerr/internal/helper/torrentmeta"
	"trackerr/internal/model"
)

// buildNamedTestTorrent is buildTestTorrent (client_test.go) parameterized by
// name, since two calls with the same name produce the same infohash and
// several sweep tests need distinct pairs on disk at once.
func buildNamedTestTorrent(t *testing.T, name string) ([]byte, string) {
	t.Helper()
	info := metainfo.Info{
		PieceLength: 16384,
		Pieces:      make([]byte, 20),
		Name:        name,
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

func TestWriteBlackholePair_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	c := &Client{config: model.ManagerConfig{BlackholeDir: dir}}
	data, hash := buildNamedTestTorrent(t, "one.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "one", Category: "movies"}

	if err := c.writeBlackholePair(data, opts); err != nil {
		t.Fatalf("writeBlackholePair: %v", err)
	}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	got, err := os.ReadFile(torrentPath)
	if err != nil {
		t.Fatalf("read torrent file: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Errorf("torrent bytes mismatch")
	}
	rawJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json file: %v", err)
	}
	var gotOpts model.ManagerAddFileOptions
	if err = json.Unmarshal(rawJSON, &gotOpts); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if gotOpts != opts {
		t.Errorf("opts mismatch: got %+v, want %+v", gotOpts, opts)
	}
}

func TestWriteBlackholePair_NoDirConfigured(t *testing.T) {
	c := &Client{}
	data, hash := buildNamedTestTorrent(t, "two.txt")
	if err := c.writeBlackholePair(data, model.ManagerAddFileOptions{Hash: hash}); err == nil {
		t.Fatal("expected error when BlackholeDir is not configured")
	}
}

func TestRemoveBlackholePair_DeletesBothFiles(t *testing.T) {
	dir := t.TempDir()
	c := &Client{config: model.ManagerConfig{BlackholeDir: dir}}
	data, hash := buildNamedTestTorrent(t, "three.txt")
	if err := c.writeBlackholePair(data, model.ManagerAddFileOptions{Hash: hash}); err != nil {
		t.Fatalf("writeBlackholePair: %v", err)
	}

	removeBlackholePair(dir, hash)

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(torrentPath); !os.IsNotExist(err) {
		t.Errorf("expected torrent file removed, stat err = %v", err)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Errorf("expected json file removed, stat err = %v", err)
	}
}

// sweepStubServer is a minimal fake qbit for the sweeper: torrents/info
// reports whatever's in `present`, torrents/add flips a hash into `present`
// (or fails outright when addFail is set).
type sweepStubServer struct {
	mu      sync.Mutex
	present map[string]bool
	addFail bool
	server  *httptest.Server
}

func newSweepStubServer(t *testing.T) *sweepStubServer {
	t.Helper()
	s := &sweepStubServer{present: map[string]bool{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("2.8.3"))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		hash := strings.ToLower(r.URL.Query().Get("hashes"))
		var list []qbt.Torrent
		if hash == "" {
			for h, ok := range s.present {
				if ok {
					list = append(list, qbt.Torrent{Hash: h})
				}
			}
		} else if s.present[hash] {
			list = append(list, qbt.Torrent{Hash: hash})
		}
		_ = json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		fail := s.addFail
		s.mu.Unlock()
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("torrents")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()
		data, _ := io.ReadAll(file)
		hash, err := torrentmeta.InfoHash(data)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.present[strings.ToLower(hash)] = true
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

func (s *sweepStubServer) markPresent(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.present[strings.ToLower(hash)] = true
}

func writePair(t *testing.T, dir string, data []byte, opts model.ManagerAddFileOptions) {
	t.Helper()
	c := &Client{config: model.ManagerConfig{BlackholeDir: dir}}
	if err := c.writeBlackholePair(data, opts); err != nil {
		t.Fatalf("writeBlackholePair: %v", err)
	}
}

func assertPairGone(t *testing.T, dir, hash string) {
	t.Helper()
	torrentPath, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(torrentPath); !os.IsNotExist(err) {
		t.Errorf("expected torrent file removed, stat err = %v", err)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Errorf("expected json file removed, stat err = %v", err)
	}
}

func assertPairRemains(t *testing.T, dir, hash string) {
	t.Helper()
	torrentPath, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(torrentPath); err != nil {
		t.Errorf("expected torrent file to remain: %v", err)
	}
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("expected json file to remain: %v", err)
	}
}

func TestSweepBlackholeOnce_SuccessRemovesPair(t *testing.T) {
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	data, hash := buildNamedTestTorrent(t, "success.txt")
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "success"})

	c.sweepBlackholeOnce(context.Background())

	assertPairGone(t, dir, hash)
}

func TestSweepBlackholeOnce_ErrorLeavesPair(t *testing.T) {
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	stub.addFail = true
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	data, hash := buildNamedTestTorrent(t, "fail.txt")
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "fail"})

	c.sweepBlackholeOnce(context.Background())

	assertPairRemains(t, dir, hash)
}

func TestSweepBlackholeOnce_AlreadyAddedRemovesPair(t *testing.T) {
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	data, hash := buildNamedTestTorrent(t, "already.txt")
	stub.markPresent(hash)
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "already"})

	c.sweepBlackholeOnce(context.Background())

	assertPairGone(t, dir, hash)
}

func TestSweepBlackholeOnce_TorrentWithoutJSONSkipped(t *testing.T) {
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	data, hash := buildNamedTestTorrent(t, "orphan.txt")
	torrentPath, _ := blackholePaths(dir, hash)
	if err = os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	c.sweepBlackholeOnce(context.Background())

	if _, err = os.Stat(torrentPath); err != nil {
		t.Errorf("expected orphan .torrent file to remain: %v", err)
	}
}

func TestSweepBlackholeOnce_ForeignExtensionUntouched(t *testing.T) {
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	foreignPath := filepath.Join(dir, "notes.txt")
	if err = os.WriteFile(foreignPath, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}

	c.sweepBlackholeOnce(context.Background())

	if _, err = os.Stat(foreignPath); err != nil {
		t.Errorf("expected foreign file to remain untouched: %v", err)
	}
}

func TestSweepBlackholeOnce_UnreachableSkipsEntirely(t *testing.T) {
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	stub.server.Close()

	data, hash := buildNamedTestTorrent(t, "down.txt")
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "down"})

	c.sweepBlackholeOnce(context.Background())

	assertPairRemains(t, dir, hash)
}

func TestSweepBlackhole_StopsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.SweepBlackhole(ctx)
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SweepBlackhole did not stop after context cancellation")
	}
}
