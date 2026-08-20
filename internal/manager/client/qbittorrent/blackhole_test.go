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

	if err := c.writeBlackholePair(data, "", opts); err != nil {
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
	if err := c.writeBlackholePair(data, "", model.ManagerAddFileOptions{Hash: hash}); err == nil {
		t.Fatal("expected error when BlackholeDir is not configured")
	}
}

func TestRemoveBlackholePair_DeletesBothFiles(t *testing.T) {
	dir := t.TempDir()
	c := &Client{config: model.ManagerConfig{BlackholeDir: dir}}
	data, hash := buildNamedTestTorrent(t, "three.txt")
	if err := c.writeBlackholePair(data, "", model.ManagerAddFileOptions{Hash: hash}); err != nil {
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

func TestWriteBlackholePair_MagnetOnly_CreatesJSONOnlyWithMagnetField(t *testing.T) {
	dir := t.TempDir()
	c := &Client{config: model.ManagerConfig{BlackholeDir: dir}}
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	magnet := "magnet:?xt=urn:btih:" + hash
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "magnet-item", Category: "movies"}

	if err := c.writeBlackholePair(nil, magnet, opts); err != nil {
		t.Fatalf("writeBlackholePair: %v", err)
	}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(torrentPath); !os.IsNotExist(err) {
		t.Errorf("expected no .torrent file for a magnet-only pair, stat err = %v", err)
	}
	rawJSON, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json file: %v", err)
	}
	var gotMeta blackholeMeta
	if err = json.Unmarshal(rawJSON, &gotMeta); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if gotMeta.Magnet != magnet {
		t.Errorf("magnet mismatch: got %q, want %q", gotMeta.Magnet, magnet)
	}
	if gotMeta.ManagerAddFileOptions != opts {
		t.Errorf("opts mismatch: got %+v, want %+v", gotMeta.ManagerAddFileOptions, opts)
	}
}

func TestWriteBlackholePair_NoDataNoMagnet_ErrorsAndCreatesNothing(t *testing.T) {
	dir := t.TempDir()
	c := &Client{config: model.ManagerConfig{BlackholeDir: dir}}
	hash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	if err := c.writeBlackholePair(nil, "", model.ManagerAddFileOptions{Hash: hash}); err == nil {
		t.Fatal("expected error when there is neither data nor magnet to protect")
	}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(torrentPath); !os.IsNotExist(err) {
		t.Errorf("expected no .torrent file, stat err = %v", err)
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Errorf("expected no .json file, stat err = %v", err)
	}
}

// sweepStubServer is a minimal fake qbit for the sweeper: torrents/info
// reports whatever's in `present`, torrents/add flips a hash into `present`
// (or fails outright when addFail is set).
type sweepStubServer struct {
	mu              sync.Mutex
	present         map[string]bool
	addFail         bool
	metadataTorrent []byte
	// vanishHash: the next torrents/info query that finds this hash present
	// removes it from `present` right after answering, so the following query
	// reports it absent. Models qbit's async delete job landing between the
	// sweeper's add/already-added check and its settle-delay recheck.
	vanishHash string
	server     *httptest.Server
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
			if s.vanishHash == hash {
				delete(s.present, hash)
			}
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
		var hash string
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
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
			h, err := torrentmeta.InfoHash(data)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			hash = h
		} else {
			if err := r.ParseForm(); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			h, ok := torrentmeta.BtihFromMagnet(r.FormValue("urls"))
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			hash = h
		}
		s.mu.Lock()
		s.present[strings.ToLower(hash)] = true
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	// backs addMagnetWithComment: real qbit answers fetchMetadata/saveMetadata
	// before the actual torrents/add, since opts.Comment is always set in
	// production (the topic URL), so that's the path a magnet re-add really takes.
	mux.HandleFunc("/api/v2/torrents/fetchMetadata", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"info":{}}`))
	})
	mux.HandleFunc("/api/v2/torrents/saveMetadata", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		data := s.metadataTorrent
		s.mu.Unlock()
		if len(data) == 0 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(data)
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

func (s *sweepStubServer) vanishAfterFirstQuery(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vanishHash = strings.ToLower(hash)
}

func writePair(t *testing.T, dir string, data []byte, opts model.ManagerAddFileOptions) {
	t.Helper()
	c := &Client{config: model.ManagerConfig{BlackholeDir: dir}}
	if err := c.writeBlackholePair(data, "", opts); err != nil {
		t.Fatalf("writeBlackholePair: %v", err)
	}
}

func writeMagnetPair(t *testing.T, dir, magnet string, opts model.ManagerAddFileOptions) {
	t.Helper()
	c := &Client{config: model.ManagerConfig{BlackholeDir: dir}}
	if err := c.writeBlackholePair(nil, magnet, opts); err != nil {
		t.Fatalf("writeBlackholePair: %v", err)
	}
}

func assertJSONGone(t *testing.T, dir, hash string) {
	t.Helper()
	_, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Errorf("expected json file removed, stat err = %v", err)
	}
}

func assertJSONRemains(t *testing.T, dir, hash string) {
	t.Helper()
	_, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("expected json file to remain: %v", err)
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
	withFastRedownloadTimings(t)
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
	withFastRedownloadTimings(t)
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

func TestSweepBlackholeOnce_MagnetPair_ReAddsAndRemovesJSON(t *testing.T) {
	withFastRedownloadTimings(t)
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	hash := "cccccccccccccccccccccccccccccccccccccccc"
	magnet := "magnet:?xt=urn:btih:" + hash
	// Comment left empty on purpose: a non-empty Comment routes AddFromMagnet
	// through addMagnetWithComment, which fetches metadata this stub can't serve.
	writeMagnetPair(t, dir, magnet, model.ManagerAddFileOptions{Hash: hash, Name: "magnet-item"})

	c.sweepBlackholeOnce(context.Background())

	assertJSONGone(t, dir, hash)
}

func TestSweepBlackholeOnce_MagnetAddError_LeavesJSON(t *testing.T) {
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	stub.addFail = true
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	hash := "dddddddddddddddddddddddddddddddddddddddd"
	magnet := "magnet:?xt=urn:btih:" + hash
	writeMagnetPair(t, dir, magnet, model.ManagerAddFileOptions{Hash: hash, Name: "magnet-item"})

	c.sweepBlackholeOnce(context.Background())

	assertJSONRemains(t, dir, hash)
}

func TestScanBlackholeDir_JSONWithoutTorrentOrMagnet_Broken(t *testing.T) {
	dir := t.TempDir()
	hash := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	_, jsonPath := blackholePaths(dir, hash)
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: model.ManagerAddFileOptions{Hash: hash}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected no valid pairs, got %+v", pairs)
	}
	if len(broken) != 1 || broken[0].hash != hash || broken[0].reason != "missing .torrent" {
		t.Errorf("expected one broken entry with reason %q, got %+v", "missing .torrent", broken)
	}
}

func TestScanBlackholeDir_OldFormatJSONWithTorrent_StillReadable(t *testing.T) {
	dir := t.TempDir()
	data, hash := buildNamedTestTorrent(t, "legacy.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "legacy", Category: "movies"}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	// old format: json.Marshal(opts) directly, no wrapping struct and no Magnet key
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal opts: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(broken) != 0 {
		t.Errorf("expected no broken entries, got %+v", broken)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected one pair, got %+v", pairs)
	}
	if !bytes.Equal(pairs[0].torrent, data) {
		t.Errorf("torrent bytes mismatch")
	}
	if pairs[0].magnet != "" {
		t.Errorf("expected empty magnet for an old-format pair, got %q", pairs[0].magnet)
	}
	if pairs[0].opts != opts {
		t.Errorf("opts mismatch: got %+v, want %+v", pairs[0].opts, opts)
	}
}

func TestScanBlackholeDir_EmptyTorrentWithMagnet_FallsBackToMagnet(t *testing.T) {
	dir := t.TempDir()
	hash := "ffffffffffffffffffffffffffffffffffffffff"
	magnet := "magnet:?xt=urn:btih:" + hash
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "half-written"}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, nil, 0o644); err != nil {
		t.Fatalf("write empty torrent: %v", err)
	}
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: opts, Magnet: magnet})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(broken) != 0 {
		t.Errorf("expected an empty .torrent with a working magnet not to be broken, got %+v", broken)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected one pair, got %+v", pairs)
	}
	if len(pairs[0].torrent) != 0 {
		t.Errorf("expected no torrent bytes, got %d", len(pairs[0].torrent))
	}
	if pairs[0].magnet != magnet {
		t.Errorf("magnet mismatch: got %q, want %q", pairs[0].magnet, magnet)
	}
}

func TestScanBlackholeDir_MagnetJSONWithFullTorrent_BytesWin(t *testing.T) {
	dir := t.TempDir()
	data, hash := buildNamedTestTorrent(t, "reoverwritten.txt")
	magnet := "magnet:?xt=urn:btih:" + hash
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "reoverwritten"}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: opts, Magnet: magnet})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(broken) != 0 {
		t.Errorf("expected no broken entries, got %+v", broken)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected one pair, got %+v", pairs)
	}
	if !bytes.Equal(pairs[0].torrent, data) {
		t.Errorf("expected torrent bytes to win over the magnet, got %v", pairs[0].torrent)
	}
}

func TestSweepBlackholeOnce_MagnetPairWithComment_ReAddsViaMetadataFetch(t *testing.T) {
	withFastRedownloadTimings(t)
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	data, hash := buildNamedTestTorrent(t, "with-comment.txt")
	stub.metadataTorrent = data
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	magnet := "magnet:?xt=urn:btih:" + hash
	// Comment set on purpose: this is the real production shape (topic URL),
	// which routes AddFromMagnet through addMagnetWithComment/fetchAndSaveMetadata.
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "with-comment", Comment: "http://tracker.example/topic/1"}
	writeMagnetPair(t, dir, magnet, opts)

	c.sweepBlackholeOnce(context.Background())

	assertJSONGone(t, dir, hash)
}

func TestScanBlackholeDir_TruncatedTorrentWithMagnet_FallsBackToMagnet(t *testing.T) {
	dir := t.TempDir()
	data, hash := buildNamedTestTorrent(t, "truncated.txt")
	magnet := "magnet:?xt=urn:btih:" + hash
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "truncated"}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	// non-empty but not valid bencode: what a write cut off mid-flight leaves behind
	if err := os.WriteFile(torrentPath, data[:5], 0o644); err != nil {
		t.Fatalf("write truncated torrent: %v", err)
	}
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: opts, Magnet: magnet})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(broken) != 0 {
		t.Errorf("expected a truncated .torrent with a working magnet not to be broken, got %+v", broken)
	}
	if len(pairs) != 1 {
		t.Fatalf("expected one pair, got %+v", pairs)
	}
	if len(pairs[0].torrent) != 0 {
		t.Errorf("expected no torrent bytes from the truncated file, got %d", len(pairs[0].torrent))
	}
	if pairs[0].magnet != magnet {
		t.Errorf("magnet mismatch: got %q, want %q", pairs[0].magnet, magnet)
	}
}

func TestScanBlackholeDir_TruncatedTorrentNoMagnet_Broken(t *testing.T) {
	dir := t.TempDir()
	data, hash := buildNamedTestTorrent(t, "truncated-no-magnet.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "truncated-no-magnet"}

	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data[:5], 0o644); err != nil {
		t.Fatalf("write truncated torrent: %v", err)
	}
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: opts})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected no valid pairs, got %+v", pairs)
	}
	if len(broken) != 1 || broken[0].hash != hash || !strings.HasPrefix(broken[0].reason, "parse torrent:") {
		t.Errorf("expected one broken entry for %s with reason prefix %q, got %+v", hash, "parse torrent:", broken)
	}
}

func TestScanBlackholeDir_HashMismatch_Broken(t *testing.T) {
	dir := t.TempDir()
	// a .torrent is present and valid on purpose: without the meta.Hash check
	// this would otherwise pass loadBlackholePair as a normal, valid pair
	data, hash := buildNamedTestTorrent(t, "ghost.txt")
	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err := os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	// meta.Hash left empty: same shape a "null" or "{}" json body would produce
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: model.ManagerAddFileOptions{Name: "ghost"}})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	pairs, broken, err := scanBlackholeDir(dir)
	if err != nil {
		t.Fatalf("scanBlackholeDir: %v", err)
	}
	if len(pairs) != 0 {
		t.Errorf("expected no valid pairs for a hash mismatch, got %+v", pairs)
	}
	wantReason := `hash mismatch: meta hash "", file ` + hash
	if len(broken) != 1 || broken[0].hash != hash || broken[0].reason != wantReason {
		t.Errorf("expected one broken entry for %s with reason %q, got %+v", hash, wantReason, broken)
	}
}

func TestSweepBlackholeOnce_DeleteEnabled_SuccessRemovesPair(t *testing.T) {
	withFastRedownloadTimings(t)
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir, DeleteEnabled: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// production shape: preAddPresenceCheck takes the waitUntilAbsent branch
	// instead of ensureNotPresent whenever DeleteEnabled is set
	data, hash := buildNamedTestTorrent(t, "delete-enabled.txt")
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "delete-enabled"})

	c.sweepBlackholeOnce(context.Background())

	assertPairGone(t, dir, hash)
}

func TestSweepBlackholeOnce_SuccessButAbsentAfterSettle_LeavesPair(t *testing.T) {
	withFastRedownloadTimings(t)
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	data, hash := buildNamedTestTorrent(t, "vanish-add.txt")
	stub.vanishAfterFirstQuery(hash)
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "vanish-add"})

	c.sweepBlackholeOnce(context.Background())

	assertPairRemains(t, dir, hash)
}

func TestSweepBlackholeOnce_AlreadyAddedButAbsentAfterSettle_LeavesPair(t *testing.T) {
	withFastRedownloadTimings(t)
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	data, hash := buildNamedTestTorrent(t, "vanish-already.txt")
	stub.markPresent(hash)
	stub.vanishAfterFirstQuery(hash)
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "vanish-already"})

	c.sweepBlackholeOnce(context.Background())

	assertPairRemains(t, dir, hash)
}

func TestSweepBlackholeOnce_BrokenPairPresentInClient_RemovesFiles(t *testing.T) {
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// orphan .torrent (no .json) is broken with reason "missing .json"
	data, hash := buildNamedTestTorrent(t, "broken-present.txt")
	torrentPath, _ := blackholePaths(dir, hash)
	if err = os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	stub.markPresent(hash)

	c.sweepBlackholeOnce(context.Background())

	if _, err = os.Stat(torrentPath); !os.IsNotExist(err) {
		t.Errorf("expected broken pair's torrent file removed since torrent is present in client, stat err = %v", err)
	}
}

func TestSweepBlackholeOnce_BrokenHashMismatchPresentInClient_KeepsFiles(t *testing.T) {
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// the bytes belong to this hash, but the json claims another one, so the
	// filename's torrent being in the client says nothing about these bytes
	data, hash := buildNamedTestTorrent(t, "mismatch-present.txt")
	torrentPath, jsonPath := blackholePaths(dir, hash)
	if err = os.WriteFile(torrentPath, data, 0o644); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	raw, err := json.Marshal(blackholeMeta{ManagerAddFileOptions: model.ManagerAddFileOptions{Name: "mismatch"}})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	if err = os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("write json: %v", err)
	}
	stub.markPresent(hash)

	c.sweepBlackholeOnce(context.Background())

	assertPairRemains(t, dir, hash)
}

func TestWriteFileAtomic_OverwritesExistingFileNoLeftoverTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}

	if err := writeFileAtomic(path, []byte("new")); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("target content = %q, want %q", got, "new")
	}
	if _, err = os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("expected no leftover .tmp file, stat err = %v", err)
	}
}

func TestWriteFileAtomic_FailedWriteLeavesTargetAndNoTmp(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, directory permissions don't block writes")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "target.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("write initial file: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("chmod dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := writeFileAtomic(path, []byte("new")); err == nil {
		t.Fatal("expected an error writing into a read-only directory")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "old" {
		t.Errorf("expected the original target untouched, got %q", got)
	}
	if _, err = os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("expected no leftover .tmp file, stat err = %v", err)
	}
}
