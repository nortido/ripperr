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

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/manager/blackhole"
	"ripperr/internal/model"
)

func buildNamedTestTorrent(t *testing.T, name string) ([]byte, string) {
	t.Helper()
	// одно имя дает один infohash, а тестам подметания нужны разные пары на диске
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

func blackholePaths(dir, hash string) (torrentPath, jsonPath string) {
	hash = strings.ToLower(hash)
	return filepath.Join(dir, hash+".torrent"), filepath.Join(dir, hash+".json")
}

type sweepStubServer struct {
	mu              sync.Mutex
	present         map[string]bool
	metadataTorrent []byte
	// последний torrents/add, файлом или по магниту: тесты AddFromMagnet по нему видят, каким путем ушло добавление
	lastAddedFile []byte
	lastAddedURL  string
	// рой мертв и для нас, и для qbit: fetchMetadata всегда отвечает 202.
	// По-другому до errMetadataFetchTimeout в waitForMetadata не дойти
	fetchMetadataForeverPending bool
	server                      *httptest.Server
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
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
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
			s.lastAddedFile = data
			s.present[strings.ToLower(hash)] = true
			s.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		magnetURL := r.FormValue("urls")
		hash, ok := torrentmeta.BtihFromMagnet(magnetURL)
		s.mu.Lock()
		s.lastAddedURL = magnetURL
		if ok {
			s.present[strings.ToLower(hash)] = true
		}
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	// настоящий qbit гоняет fetchMetadata/saveMetadata до torrents/add. Comment в бою есть всегда
	// (URL темы), так что магнит заново добавляется как раз этим путем
	mux.HandleFunc("/api/v2/torrents/fetchMetadata", func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		pending := s.fetchMetadataForeverPending
		s.mu.Unlock()
		if pending {
			w.WriteHeader(http.StatusAccepted)
			return
		}
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

func writePair(t *testing.T, dir string, data []byte, opts model.ManagerAddFileOptions) {
	t.Helper()
	if err := blackhole.New(dir, nil).Write(data, "", opts); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ageJSON(t, dir, opts.Hash)
}

func writeMagnetPair(t *testing.T, dir, magnet string, opts model.ManagerAddFileOptions) {
	t.Helper()
	if err := blackhole.New(dir, nil).Write(nil, magnet, opts); err != nil {
		t.Fatalf("Write: %v", err)
	}
	ageJSON(t, dir, opts.Hash)
}

func ageJSON(t *testing.T, dir, hash string) {
	t.Helper()
	_, jsonPath := blackholePaths(dir, hash)
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(jsonPath, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

func assertJSONGone(t *testing.T, dir, hash string) {
	t.Helper()
	_, jsonPath := blackholePaths(dir, hash)
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Errorf("expected json file removed, stat err = %v", err)
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
	// Comment как в бою (URL темы), он уводит AddFromMagnet в addMagnetWithComment
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "with-comment", Comment: "http://tracker.example/topic/1"}
	writeMagnetPair(t, dir, magnet, opts)

	c.bh.SweepOnce(context.Background())

	assertJSONGone(t, dir, hash)
}

func TestSweepBlackholeOnce_DeleteEnabled_SuccessRemovesPair(t *testing.T) {
	withFastRedownloadTimings(t)
	dir := t.TempDir()
	stub := newSweepStubServer(t)
	c, err := New(model.ManagerConfig{Host: stub.server.URL, BlackholeDir: dir, DeleteEnabled: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// как в бою: с DeleteEnabled preAddPresenceCheck идет через waitUntilAbsent
	data, hash := buildNamedTestTorrent(t, "delete-enabled.txt")
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "delete-enabled"})

	c.bh.SweepOnce(context.Background())

	assertPairGone(t, dir, hash)
}
