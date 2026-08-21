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

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	qbt "github.com/autobrr/go-qbittorrent"

	"trackerr/internal/helper/torrentmeta"
	"trackerr/internal/manager/blackhole"
	"trackerr/internal/model"
)

// buildNamedTestTorrent это buildTestTorrent (client_test.go), параметризованный
// именем, потому что два вызова с одинаковым именем дают один и тот же infohash,
// а нескольким тестам подметания нужны разные пары на диске одновременно.
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

// blackholePaths это зеркало приватной раскладки путей пакета blackhole,
// нужное только тестам для сборки ожидаемых путей на диске.
func blackholePaths(dir, hash string) (torrentPath, jsonPath string) {
	hash = strings.ToLower(hash)
	return filepath.Join(dir, hash+".torrent"), filepath.Join(dir, hash+".json")
}

// sweepStubServer это минимальный поддельный qbit, нужный только тесту с
// addMagnetWithComment/fetchMetadata: torrents/info сообщает то, что лежит в
// present, torrents/add переводит хеш в present, fetchMetadata и saveMetadata
// обслуживают путь fetchAndSaveMetadata.
type sweepStubServer struct {
	mu              sync.Mutex
	present         map[string]bool
	metadataTorrent []byte
	server          *httptest.Server
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
	// обслуживает addMagnetWithComment: настоящий qbit отвечает на
	// fetchMetadata/saveMetadata до самого torrents/add, так как opts.Comment
	// в бою выставлен всегда (URL темы), значит именно этим путём и идёт
	// повторное добавление магнита.
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

func writePair(t *testing.T, dir string, data []byte, opts model.ManagerAddFileOptions) {
	t.Helper()
	if err := blackhole.New(dir, nil).Write(data, "", opts); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

func writeMagnetPair(t *testing.T, dir, magnet string, opts model.ManagerAddFileOptions) {
	t.Helper()
	if err := blackhole.New(dir, nil).Write(nil, magnet, opts); err != nil {
		t.Fatalf("Write: %v", err)
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
	// Comment выставлен намеренно: это боевая форма (URL темы), которая уводит
	// AddFromMagnet в addMagnetWithComment/fetchAndSaveMetadata.
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

	// боевая форма: preAddPresenceCheck идёт веткой waitUntilAbsent вместо
	// ensureNotPresent всякий раз, когда выставлен DeleteEnabled
	data, hash := buildNamedTestTorrent(t, "delete-enabled.txt")
	writePair(t, dir, data, model.ManagerAddFileOptions{Hash: hash, Name: "delete-enabled"})

	c.bh.SweepOnce(context.Background())

	assertPairGone(t, dir, hash)
}
