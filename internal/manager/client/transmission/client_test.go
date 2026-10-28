package transmission

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
)

func withFastPresenceTimings(t *testing.T) {
	t.Helper()
	origAttempts, origDelay := presenceCheckAttempts, presenceCheckDelay
	presenceCheckAttempts = 2
	presenceCheckDelay = 10 * time.Millisecond
	t.Cleanup(func() { presenceCheckAttempts, presenceCheckDelay = origAttempts, origDelay })
}

func buildTestTorrent(t *testing.T, name string) ([]byte, string) {
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
	if err = mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes(), mi.HashInfoBytes().HexString()
}

func writeResult(w http.ResponseWriter, args interface{}) {
	raw, _ := json.Marshal(args)
	_ = json.NewEncoder(w).Encode(rpcResponse{Result: "success", Arguments: raw})
}

type stubServer struct {
	mu          sync.Mutex
	downloadDir string
	torrents    map[string]rpcTorrent
	addCalls    []map[string]interface{}
	removeCalls []map[string]interface{}
	setCalls    []map[string]interface{}
	methods     []string
	startCalls  []map[string]interface{}
	// torrent-get с полями files или fileStats
	fileGets int
	// достаются торренту, созданному torrent-add
	addFiles     []rpcFile
	addFileStats []rpcFileStat
	// torrent-set с files-unwanted отвечает ошибкой
	failSetUnwanted bool
	// torrent-start отвечает ошибкой
	failStart bool
	// торрент на деле не добавляется, как при молчаливом отказе Transmission
	failEnsurePresent bool
	// torrent-add с этим хешем отвечает torrent-duplicate, как настоящий
	// Transmission на повторное добавление, и торренты не трогает
	duplicateFor string
	// torrent-get с этим хешем падает: сбой сети в categoryInsideBase перед удалением
	failTorrentGetFor string
	server            *httptest.Server
}

func newStubServer(t *testing.T, downloadDir string) *stubServer {
	t.Helper()
	s := &stubServer{downloadDir: downloadDir, torrents: map[string]rpcTorrent{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method    string                 `json:"method"`
			Arguments map[string]interface{} `json:"arguments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.methods = append(s.methods, req.Method)
		switch req.Method {
		case "session-get":
			writeResult(w, map[string]interface{}{"download-dir": s.downloadDir})
		case "torrent-get":
			if fields, ok := req.Arguments["fields"].([]interface{}); ok {
				for _, f := range fields {
					if f == "files" || f == "fileStats" {
						s.fileGets++
						break
					}
				}
			}
			var list []rpcTorrent
			if idsRaw, ok := req.Arguments["ids"]; ok {
				for _, idAny := range idsRaw.([]interface{}) {
					if s.failTorrentGetFor != "" && strings.EqualFold(idAny.(string), s.failTorrentGetFor) {
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					if t, ok := s.torrents[strings.ToLower(idAny.(string))]; ok {
						list = append(list, t)
					}
				}
			} else {
				for _, t := range s.torrents {
					list = append(list, t)
				}
			}
			writeResult(w, map[string]interface{}{"torrents": list})
		case "torrent-add":
			s.addCalls = append(s.addCalls, req.Arguments)
			hash, ok := s.hashFromAddArgs(req.Arguments)
			if !ok {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if s.duplicateFor != "" && strings.EqualFold(hash, s.duplicateFor) {
				writeResult(w, map[string]interface{}{"torrent-duplicate": map[string]interface{}{"hashString": hash}})
				return
			}
			if !s.failEnsurePresent {
				dl, _ := req.Arguments["download-dir"].(string)
				var labels []string
				if raw, ok := req.Arguments["labels"]; ok && raw != nil {
					for _, l := range raw.([]interface{}) {
						labels = append(labels, l.(string))
					}
				}
				s.torrents[hash] = rpcTorrent{HashString: hash, DownloadDir: dl, Labels: labels}
				if s.addFiles != nil {
					s.torrents[hash] = rpcTorrent{HashString: hash, DownloadDir: dl, Labels: labels, Files: s.addFiles, FileStats: s.addFileStats}
				}
			}
			writeResult(w, map[string]interface{}{"torrent-added": map[string]interface{}{"hashString": hash}})
		case "torrent-remove":
			s.removeCalls = append(s.removeCalls, req.Arguments)
			if idsRaw, ok := req.Arguments["ids"]; ok {
				for _, idAny := range idsRaw.([]interface{}) {
					delete(s.torrents, strings.ToLower(idAny.(string)))
				}
			}
			writeResult(w, map[string]interface{}{})
		case "torrent-set":
			if _, ok := req.Arguments["files-unwanted"]; ok && s.failSetUnwanted {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			s.setCalls = append(s.setCalls, req.Arguments)
			writeResult(w, map[string]interface{}{})
		case "torrent-start":
			if s.failStart {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			s.startCalls = append(s.startCalls, req.Arguments)
			writeResult(w, map[string]interface{}{})
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	s.server = httptest.NewServer(mux)
	t.Cleanup(s.server.Close)
	return s
}

func (s *stubServer) hashFromAddArgs(args map[string]interface{}) (string, bool) {
	if raw, ok := args["metainfo"].(string); ok && raw != "" {
		data, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return "", false
		}
		hash, err := torrentmeta.InfoHash(data)
		if err != nil {
			return "", false
		}
		return strings.ToLower(hash), true
	}
	if magnet, ok := args["filename"].(string); ok && magnet != "" {
		hash, ok := torrentmeta.BtihFromMagnet(magnet)
		return strings.ToLower(hash), ok
	}
	return "", false
}

func (s *stubServer) putTorrent(t rpcTorrent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.torrents[strings.ToLower(t.HashString)] = t
}

func newTestClient(t *testing.T, stub *stubServer, cfg model.ManagerConfig) *Client {
	t.Helper()
	cfg.Host = stub.server.URL
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestCategoryFromPath(t *testing.T) {
	cases := []struct {
		name        string
		downloadDir string
		base        string
		wantCat     string
		wantInside  bool
	}{
		{"equal", "/downloads", "/downloads", "", true},
		{"equal trailing slash", "/downloads/", "/downloads", "", true},
		{"one level inside", "/downloads/movies", "/downloads", "movies", true},
		// категория только первый сегмент (tv), иначе у каждого торрента своя
		{"two levels inside", "/downloads/tv/show1", "/downloads", "tv", true},
		{"outside base", "/other/place", "/downloads", "place", false},
		// basename как у категории внутри базы, но для Delete их надо различать
		{"outside base same name as inside category", "/mnt/archive/movies", "/downloads", "movies", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotCat, gotInside := categoryFromPath(c.downloadDir, c.base)
			if gotCat != c.wantCat || gotInside != c.wantInside {
				t.Errorf("categoryFromPath(%q, %q) = (%q, %v), want (%q, %v)", c.downloadDir, c.base, gotCat, gotInside, c.wantCat, c.wantInside)
			}
		})
	}
}

func TestDestinationDir(t *testing.T) {
	cases := []struct {
		name string
		opts model.ManagerAddFileOptions
		want string
	}{
		{"save path set", model.ManagerAddFileOptions{SavePath: "/exact/path", Category: "movies"}, "/exact/path"},
		{"category only", model.ManagerAddFileOptions{Category: "movies"}, "/downloads/movies"},
		{"neither set", model.ManagerAddFileOptions{}, "/downloads"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := destinationDir(c.opts, "/downloads"); got != c.want {
				t.Errorf("destinationDir() = %q, want %q", got, c.want)
			}
		})
	}

	// session-get не отдал базовый каталог: относительный путь получиться не должен
	emptyBaseCases := []struct {
		name string
		opts model.ManagerAddFileOptions
		want string
	}{
		{"empty base, neither set", model.ManagerAddFileOptions{}, ""},
		{"empty base, category only", model.ManagerAddFileOptions{Category: "movies"}, ""},
		{"empty base, save path set", model.ManagerAddFileOptions{SavePath: "/exact/path"}, "/exact/path"},
	}
	for _, c := range emptyBaseCases {
		t.Run(c.name, func(t *testing.T) {
			if got := destinationDir(c.opts, ""); got != c.want {
				t.Errorf("destinationDir() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestMapEntry(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{PausedMaxRatio: 0.5})

	rt := &rpcTorrent{
		HashString:     "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
		Name:           "movie",
		Comment:        "http://tracker.example/topic/42",
		DownloadDir:    "/downloads/movies",
		Labels:         []string{"one", "two"},
		AddedDate:      1700000000,
		PercentDone:    1,
		Status:         0,
		SeedRatioLimit: 2.5,
		SeedRatioMode:  1,
	}
	e := c.mapEntry(rt)

	if e.Hash != strings.ToLower(rt.HashString) {
		t.Errorf("hash not lowered: got %q", e.Hash)
	}
	if e.Tag != "one,two" {
		t.Errorf("tag: got %q", e.Tag)
	}
	if e.URL != rt.Comment || e.TopicID != "42" {
		t.Errorf("url/topicID: got %q / %q", e.URL, e.TopicID)
	}
	if e.Timestamp == nil || !e.Timestamp.Equal(time.Unix(1700000000, 0)) {
		t.Errorf("timestamp: got %v", e.Timestamp)
	}
	if e.AutoManaged {
		t.Error("AutoManaged should always be false")
	}
	if e.MaxSeedingTime != 0 {
		t.Error("MaxSeedingTime should always be zero")
	}
	if e.ContentPath != "/downloads/movies/movie" {
		t.Errorf("ContentPath: got %q", e.ContentPath)
	}
	if got := c.mapEntry(&rpcTorrent{Name: "movie"}).ContentPath; got != "" {
		t.Errorf("ContentPath without download dir: got %q, want empty", got)
	}
	// остановлен и докачан: PausedMaxRatio перекрывает seedRatioMode
	if e.MaxRatio != 0.5 {
		t.Errorf("expected paused max ratio override, got %v", e.MaxRatio)
	}
}

func TestMapEntry_SeedRatioModes(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{PausedMaxRatio: 0.5})

	cases := []struct {
		mode   int
		status int
		done   float64
		limit  float64
		want   float64
	}{
		// соглашение qBittorrent: -1 без ограничения (Transmission mode 2), -2
		// глобальный лимит клиента (Transmission mode 0)
		{mode: 0, status: 4, done: 0.5, limit: 2, want: -2},
		{mode: 1, status: 4, done: 0.5, limit: 2, want: 2},
		{mode: 2, status: 4, done: 0.5, limit: 2, want: -1},
		// остановлена и докачана -> PausedMaxRatio поверх режима
		{mode: 1, status: 0, done: 1, limit: 2, want: 0.5},
	}
	for _, cse := range cases {
		rt := &rpcTorrent{DownloadDir: "/downloads", SeedRatioMode: cse.mode, Status: cse.status, PercentDone: cse.done, SeedRatioLimit: cse.limit}
		if got := c.mapEntry(rt).MaxRatio; got != cse.want {
			t.Errorf("mode=%d status=%d done=%v: got MaxRatio=%v, want %v", cse.mode, cse.status, cse.done, got, cse.want)
		}
	}
}

func TestAddFromBuffer_SendsDownloadDirAndLabels(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})

	data, hash := buildTestTorrent(t, "add-buffer.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "add-buffer", Category: "movies", Tag: "a, b ,, c"}

	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}

	if len(stub.addCalls) != 1 {
		t.Fatalf("expected 1 add call, got %d", len(stub.addCalls))
	}
	call := stub.addCalls[0]
	if call["download-dir"] != "/downloads/movies" {
		t.Errorf("download-dir: got %v", call["download-dir"])
	}
	labels, _ := call["labels"].([]interface{})
	if len(labels) != 3 || labels[0] != "a" || labels[1] != "b" || labels[2] != "c" {
		t.Errorf("labels: got %v", labels)
	}
}

func TestAddArgs_OmitsEmptyKeysInsteadOfNull(t *testing.T) {
	// пустой Tag: ключа labels в аргументах нет вовсе (не null)
	args := addArgs(model.ManagerAddFileOptions{}, "/downloads")
	if _, ok := args["labels"]; ok {
		t.Errorf("expected no labels key for empty tag, got %v", args["labels"])
	}
	if args["download-dir"] != "/downloads" {
		t.Errorf("download-dir: got %v", args["download-dir"])
	}

	// пустой базовый каталог сессии и SavePath: относительный путь не собирается,
	// ключа download-dir нет вовсе
	args = addArgs(model.ManagerAddFileOptions{}, "")
	if _, ok := args["download-dir"]; ok {
		t.Errorf("expected no download-dir key for empty base, got %v", args["download-dir"])
	}

	args = addArgs(model.ManagerAddFileOptions{Tag: "a,b"}, "/downloads")
	if labels, _ := args["labels"].([]string); len(labels) != 2 {
		t.Errorf("labels: got %v", args["labels"])
	}
}

func TestAddFromBuffer_EmptyBaseOmitsDownloadDirKey(t *testing.T) {
	withFastPresenceTimings(t)
	// пустой download-dir бывает и у настоящего Transmission при странном ответе
	stub := newStubServer(t, "")
	c := newTestClient(t, stub, model.ManagerConfig{})

	data, hash := buildTestTorrent(t, "no-base.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "no-base"}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}

	if len(stub.addCalls) != 1 {
		t.Fatalf("expected 1 add call, got %d", len(stub.addCalls))
	}
	if _, ok := stub.addCalls[0]["download-dir"]; ok {
		t.Errorf("expected no download-dir key sent, got %v", stub.addCalls[0]["download-dir"])
	}
}

func TestAddFromBuffer_EmptyTagOmitsLabelsKey(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})

	data, hash := buildTestTorrent(t, "no-tag.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "no-tag"}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}

	if len(stub.addCalls) != 1 {
		t.Fatalf("expected 1 add call, got %d", len(stub.addCalls))
	}
	if _, ok := stub.addCalls[0]["labels"]; ok {
		t.Errorf("expected no labels key sent, got %v", stub.addCalls[0]["labels"])
	}
}

func TestAddFromBuffer_AlreadyPresentDifferentHash(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "/downloads")
	data, hash := buildTestTorrent(t, "already-present.txt")
	stub.putTorrent(rpcTorrent{HashString: strings.ToLower(hash), DownloadDir: "/downloads"})

	c := newTestClient(t, stub, model.ManagerConfig{})

	// другой opts.Hash: та же ветка, что "чужой торрент уже занял инфохеш"
	opts := model.ManagerAddFileOptions{Hash: "deadbeef", Name: "already-present"}
	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts)
	if !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("expected ErrAlreadyAdded, got %v", err)
	}
}

func TestAddFromBuffer_NotPresentAfterAdd(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "/downloads")
	stub.failEnsurePresent = true
	c := newTestClient(t, stub, model.ManagerConfig{})

	data, hash := buildTestTorrent(t, "vanishes.txt")
	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: hash, Name: "vanishes"})
	if err == nil {
		t.Fatal("expected error when torrent never appears after add")
	}
}

func TestAddFromBuffer_AppliesMaxRatio(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})

	data, hash := buildTestTorrent(t, "ratio.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "ratio", MaxRatio: 3.5}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if len(stub.setCalls) != 1 {
		t.Fatalf("expected 1 torrent-set call, got %d", len(stub.setCalls))
	}
	if stub.setCalls[0]["seedRatioLimit"] != 3.5 {
		t.Errorf("seedRatioLimit: got %v", stub.setCalls[0]["seedRatioLimit"])
	}
	if stub.setCalls[0]["seedRatioMode"] != float64(1) {
		t.Errorf("seedRatioMode: got %v, want 1 (custom limit)", stub.setCalls[0]["seedRatioMode"])
	}
}

func TestAddFromBuffer_AppliesMaxRatio_NoLimit(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})

	data, hash := buildTestTorrent(t, "no-limit.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "no-limit", MaxRatio: -1}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if len(stub.setCalls) != 1 {
		t.Fatalf("expected 1 torrent-set call, got %d", len(stub.setCalls))
	}
	if stub.setCalls[0]["seedRatioMode"] != float64(2) {
		t.Errorf("seedRatioMode: got %v, want 2 (no limit)", stub.setCalls[0]["seedRatioMode"])
	}
}

func TestAddFromBuffer_AppliesMaxRatio_GlobalLimit(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})

	data, hash := buildTestTorrent(t, "global-limit.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "global-limit", MaxRatio: -2}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if len(stub.setCalls) != 1 {
		t.Fatalf("expected 1 torrent-set call, got %d", len(stub.setCalls))
	}
	if stub.setCalls[0]["seedRatioMode"] != float64(0) {
		t.Errorf("seedRatioMode: got %v, want 0 (global)", stub.setCalls[0]["seedRatioMode"])
	}
}

func TestAddFromBuffer_ZeroMaxRatioSkipsTorrentSet(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})

	data, hash := buildTestTorrent(t, "zero-ratio.txt")
	opts := model.ManagerAddFileOptions{Hash: hash, Name: "zero-ratio"}
	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if len(stub.setCalls) != 0 {
		t.Errorf("expected no torrent-set call for MaxRatio=0, got %d", len(stub.setCalls))
	}
}

func TestAddFromBuffer_DuplicateResponseIsAlreadyAdded(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "/downloads")
	data, hash := buildTestTorrent(t, "dup.txt")
	// на дубликат приходит success с torrent-duplicate. Без разбора этого ключа
	// ensurePresentAfterAdd видит старый торрент и рапортует успех
	stub.duplicateFor = hash
	c := newTestClient(t, stub, model.ManagerConfig{})

	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: hash, Name: "dup"})
	if !errors.Is(err, model.ErrAlreadyAdded) {
		t.Fatalf("expected ErrAlreadyAdded, got %v", err)
	}
}

func TestDelete_DisabledDoesNothing(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: false})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Name: "x"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 0 {
		t.Errorf("expected no remove calls, got %d", len(stub.removeCalls))
	}
}

func TestDelete_CategoryInListDeletesFiles(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	stub.putTorrent(rpcTorrent{HashString: "h1", DownloadDir: "/downloads/movies"})
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "movies", Name: "x", DeleteFiles: true}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != true {
		t.Fatalf("expected delete-local-data=true, got %v", stub.removeCalls)
	}
}

func TestDelete_CategoryMatchesButOutsideBaseKeepsFiles(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	// категория та же, что у /downloads/movies, но кто добавил "movies" в список
	// имел ввиду каталог внутри базы. Файлы снаружи не трогаем
	stub.putTorrent(rpcTorrent{HashString: "h1", DownloadDir: "/mnt/archive/movies"})
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "movies", Name: "x", DeleteFiles: true}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected delete-local-data=false for a category outside the base dir, got %v", stub.removeCalls)
	}
}

func TestDelete_CategoryNotInListKeepsFiles(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "tv", Name: "x", DeleteFiles: true}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected delete-local-data=false, got %v", stub.removeCalls)
	}
}

func TestDelete_CategoryInsideBaseCheckFailsKeepsFiles(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	// где лежит раздача, неизвестно, значит файлы не удаляем
	stub.failTorrentGetFor = "h1"
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "movies", Name: "x", DeleteFiles: true}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected delete-local-data=false when the downloadDir check fails, got %v", stub.removeCalls)
	}
}

func TestDelete_TorrentAbsentFromCheckKeepsFiles(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	// раздачи в клиенте уже нет, torrentByHash вернет nil без ошибки. Файлы тоже не трогаем
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "movies", Name: "x", DeleteFiles: true}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected delete-local-data=false when the torrent is absent, got %v", stub.removeCalls)
	}
}

func TestForceRedownload_Unsupported(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})

	if err := c.ForceRedownload(context.Background(), "somehash", model.RedownloadOptions{}); !errors.Is(err, ErrRedownloadUnsupported) {
		t.Fatalf("expected ErrRedownloadUnsupported, got %v", err)
	}
}

func TestGetCategories(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	stub.putTorrent(rpcTorrent{HashString: "h1", DownloadDir: "/downloads"})
	stub.putTorrent(rpcTorrent{HashString: "h2", DownloadDir: "/downloads/movies"})
	stub.putTorrent(rpcTorrent{HashString: "h3", DownloadDir: "/downloads/tv"})
	stub.putTorrent(rpcTorrent{HashString: "h4", DownloadDir: "/downloads/movies"})

	c := newTestClient(t, stub, model.ManagerConfig{})
	cats, err := c.GetCategories(context.Background())
	if err != nil {
		t.Fatalf("GetCategories: %v", err)
	}
	if len(cats) != 2 || cats[0] != "movies" || cats[1] != "tv" {
		t.Fatalf("got %v", cats)
	}
}

func TestExistsAndReachable(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	stub.putTorrent(rpcTorrent{HashString: "abc123"})
	c := newTestClient(t, stub, model.ManagerConfig{})

	exists, err := c.Exists(context.Background(), "abc123")
	if err != nil || !exists {
		t.Fatalf("Exists: got %v, %v", exists, err)
	}
	exists, err = c.Exists(context.Background(), "missing")
	if err != nil || exists {
		t.Fatalf("Exists(missing): got %v, %v", exists, err)
	}
	if !c.Reachable(context.Background()) {
		t.Fatal("expected Reachable to be true")
	}
}

func TestIncomingHashSameAs(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})

	data, hash := buildTestTorrent(t, "same-hash.txt")
	same, known := c.IncomingHashSameAs(bytes.NewBuffer(data), "", hash)
	if !same || !known {
		t.Errorf("got same=%v known=%v", same, known)
	}
}

func TestIncomingHashSameAs_NilBufferWithMagnet(t *testing.T) {
	// контракт допускает buf == nil вместе с магнитом: без проверки на nil
	// такой вызов паникует
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})

	hash := "0123456789abcdef0123456789abcdef01234567"
	magnet := "magnet:?xt=urn:btih:" + hash

	same, known := c.IncomingHashSameAs(nil, magnet, hash)
	if !same || !known {
		t.Errorf("got same=%v known=%v, want same=true known=true", same, known)
	}
}

func TestIncomingHashSameAs_NilBufferNoMagnet(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})

	hash := "0123456789abcdef0123456789abcdef01234567"
	same, known := c.IncomingHashSameAs(nil, "", hash)
	if same || known {
		t.Errorf("got same=%v known=%v, want same=false known=false", same, known)
	}
}

func TestDeletesFiles(t *testing.T) {
	all := model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"movies"}}
	cases := []struct {
		name        string
		cfg         model.ManagerConfig
		downloadDir string
		category    string
		want        bool
	}{
		{"inside base", all, "/downloads/movies", "movies", true},
		{"outside base", all, "/mnt/archive/movies", "movies", false},
		{"category not listed", all, "/downloads/tv", "tv", false},
		{"files disabled", model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}}, "/downloads/movies", "movies", false},
		{"delete disabled", model.ManagerConfig{DeleteFilesEnabled: true, DeleteCategories: []string{"movies"}}, "/downloads/movies", "movies", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStubServer(t, "/downloads")
			stub.putTorrent(rpcTorrent{HashString: "h1", DownloadDir: tc.downloadDir})
			c := newTestClient(t, stub, tc.cfg)
			if got := c.DeletesFiles(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: tc.category}); got != tc.want {
				t.Fatalf("DeletesFiles = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWaitAbsent(t *testing.T) {
	withFastPresenceTimings(t)
	stub := newStubServer(t, "/downloads")
	stub.putTorrent(rpcTorrent{HashString: "h1"})
	c := newTestClient(t, stub, model.ManagerConfig{})

	if absent, err := c.WaitAbsent(context.Background(), "h1"); err != nil || absent {
		t.Fatalf("WaitAbsent(present) = %v, %v; want false, nil", absent, err)
	}
	if absent, err := c.WaitAbsent(context.Background(), "missing"); err != nil || !absent {
		t.Fatalf("WaitAbsent(missing) = %v, %v; want true, nil", absent, err)
	}
}

func TestAddArgs_StoppedSendsPaused(t *testing.T) {
	args := addArgs(model.ManagerAddFileOptions{Stopped: true}, "/downloads")
	if args["paused"] != true {
		t.Errorf("paused: got %v, want true", args["paused"])
	}
	args = addArgs(model.ManagerAddFileOptions{}, "/downloads")
	if _, ok := args["paused"]; ok {
		t.Errorf("expected no paused key for a started add, got %v", args["paused"])
	}
}

func TestDiscard_NeverDeletesLocalData(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	stub.putTorrent(rpcTorrent{HashString: "h1", DownloadDir: "/downloads/movies"})
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Discard(context.Background(), "h1"); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected one torrent-remove with delete-local-data=false, got %v", stub.removeCalls)
	}
}

func TestPurge_DeletesLocalDataWhateverSettings(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	stub.putTorrent(rpcTorrent{HashString: "h1", DownloadDir: "/elsewhere/movies"})
	c := newTestClient(t, stub, model.ManagerConfig{})

	if err := c.Purge(context.Background(), "h1"); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if len(stub.removeCalls) != 1 || !idsAre(stub.removeCalls[0]["ids"], "h1") || stub.removeCalls[0]["delete-local-data"] != true {
		t.Fatalf("expected one torrent-remove of h1 with delete-local-data=true, got %v", stub.removeCalls)
	}
	if slices.Contains(stub.methods, "torrent-get") {
		t.Fatalf("Purge read torrents: %v", stub.methods)
	}
}

func withFastCheckWait(t *testing.T, budget time.Duration) {
	t.Helper()
	origDelay, origBudget := checkWaitDelay, checkWaitBudget
	checkWaitDelay, checkWaitBudget = 5*time.Millisecond, budget
	t.Cleanup(func() { checkWaitDelay, checkWaitBudget = origDelay, origBudget })
}

func newResumeClient(t *testing.T, statuses []int, afterErr int, afterErrString string) (*Client, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var methods []string
	started := false
	mux := http.NewServeMux()
	mux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch req.Method {
		case "session-get":
			writeResult(w, map[string]interface{}{"download-dir": "/downloads"})
			return
		case "torrent-start":
			started = true
		case "torrent-get":
			tor := map[string]interface{}{"hashString": "h1", "status": 0, "error": afterErr, "errorString": afterErrString}
			if !started {
				tor = map[string]interface{}{"hashString": "h1", "status": statuses[0], "error": 0, "errorString": ""}
				if len(statuses) > 1 {
					statuses = statuses[1:]
				}
			}
			methods = append(methods, req.Method)
			writeResult(w, map[string]interface{}{"torrents": []interface{}{tor}})
			return
		}
		methods = append(methods, req.Method)
		writeResult(w, map[string]interface{}{})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	c, err := New(model.ManagerConfig{Host: server.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), methods...)
	}
}

func TestResume_VerifiesWaitsThenStarts(t *testing.T) {
	withFastCheckWait(t, time.Second)
	c, methods := newResumeClient(t, []int{0, 2, 2, 0}, 0, "")
	if err := c.Resume(context.Background(), "h1"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	want := "torrent-get,torrent-verify,torrent-get,torrent-get,torrent-get,torrent-start,torrent-get"
	if got := strings.Join(methods(), ","); got != want {
		t.Fatalf("methods = %s, want %s", got, want)
	}
}

func TestResume_LocalErrorAfterStartIsError(t *testing.T) {
	withFastCheckWait(t, time.Second)
	c, _ := newResumeClient(t, []int{0}, 3, "No data found!")
	if err := c.Resume(context.Background(), "h1"); err == nil || !strings.Contains(err.Error(), "No data found!") {
		t.Fatalf("err = %v, want No data found!", err)
	}
}

func TestResume_TrackerErrorAfterStartIsFine(t *testing.T) {
	// ошибка трекера не про данные: падение на ней держало бы пару вечно
	for _, code := range []int{1, 2} {
		withFastCheckWait(t, time.Second)
		c, methods := newResumeClient(t, []int{0}, code, "tracker says no")
		if err := c.Resume(context.Background(), "h1"); err != nil {
			t.Errorf("error %d: Resume = %v, want nil", code, err)
		}
		if got := methods(); len(got) < 2 || got[len(got)-2] != "torrent-start" {
			t.Errorf("error %d: methods = %v, want torrent-start before the last read", code, got)
		}
	}
}

func TestResume_CheckPastBudgetNeverStarts(t *testing.T) {
	withFastCheckWait(t, 50*time.Millisecond)
	c, methods := newResumeClient(t, []int{1, 2}, 0, "")
	if err := c.Resume(context.Background(), "h1"); err == nil {
		t.Fatal("expected a timeout error")
	}
	for _, m := range methods() {
		if m == "torrent-start" {
			t.Fatalf("torrent-start was sent: %v", methods())
		}
	}
}

func TestResume_RunningTorrentNoVerify(t *testing.T) {
	// повторная проверка запущенного торрента на каждом тике подметальщика
	// гоняла бы диск и держала пару вечно
	withFastCheckWait(t, time.Second)
	c, methods := newResumeClient(t, []int{4}, 0, "")
	if err := c.Resume(context.Background(), "h1"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := strings.Join(methods(), ","); got != "torrent-get" {
		t.Fatalf("methods = %s, want torrent-get only", got)
	}
}

func TestResume_PausedAddStillVerifyingWaitsThenStarts(t *testing.T) {
	// остановленное добавление само сверяет файлы: ждем конца, потом
	// проверяем заново уже без файлов старого и запускаем
	withFastCheckWait(t, time.Second)
	c, methods := newResumeClient(t, []int{2, 0, 1, 0}, 0, "")
	if err := c.Resume(context.Background(), "h1"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	want := "torrent-get,torrent-get,torrent-verify,torrent-get,torrent-get,torrent-start,torrent-get"
	if got := strings.Join(methods(), ","); got != want {
		t.Fatalf("methods = %s, want %s", got, want)
	}
}

func TestFiles_ZipsFilesAndStats(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	stub.putTorrent(rpcTorrent{HashString: "h1", Files: []rpcFile{{Name: "R/a"}, {Name: "R/b"}}, FileStats: []rpcFileStat{{Wanted: true}, {Wanted: false}}})
	stub.putTorrent(rpcTorrent{HashString: "h2", Files: []rpcFile{{Name: "R/a"}, {Name: "R/b"}}, FileStats: []rpcFileStat{{Wanted: true}}})
	c := newTestClient(t, stub, model.ManagerConfig{})

	got, err := c.Files(context.Background(), "h1")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := []model.TorrentFile{{Path: "R/a", Wanted: true}, {Path: "R/b", Wanted: false}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Files: got %v, want %v", got, want)
	}
	if stub.fileGets != 1 {
		t.Errorf("expected 1 torrent-get with file fields, got %d", stub.fileGets)
	}
	if _, err = c.Files(context.Background(), "h2"); err == nil {
		t.Error("expected an error for files/fileStats length mismatch")
	}
	if _, err = c.Files(context.Background(), "absent"); err == nil {
		t.Error("expected an error for an absent torrent")
	}
}

func TestRPCFileStat_WantedAcceptsBoolAndNumber(t *testing.T) {
	var stats []rpcFileStat
	if err := json.Unmarshal([]byte(`[{"wanted":true},{"wanted":false},{"wanted":1},{"wanted":0}]`), &stats); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	want := []bool{true, false, true, false}
	if len(stats) != len(want) {
		t.Fatalf("got %d stats, want %d", len(stats), len(want))
	}
	for i, w := range want {
		if bool(stats[i].Wanted) != w {
			t.Errorf("stats[%d].Wanted: got %v, want %v", i, stats[i].Wanted, w)
		}
	}
	if err := json.Unmarshal([]byte(`[{"wanted":"1"}]`), &stats); err == nil {
		t.Error("expected an error for a string wanted value")
	}
}

func newSelectionStub(t *testing.T) (*stubServer, *Client, []byte, string) {
	t.Helper()
	withFastPresenceTimings(t)
	stub := newStubServer(t, "/downloads")
	stub.addFiles = []rpcFile{{Name: "R2/a"}, {Name: "R2/b"}, {Name: "R2/c"}}
	stub.addFileStats = []rpcFileStat{{Wanted: true}, {Wanted: true}, {Wanted: true}}
	c := newTestClient(t, stub, model.ManagerConfig{})
	data, hash := buildTestTorrent(t, "selection.txt")
	return stub, c, data, hash
}

func selectionOpts() model.ManagerAddFileOptions {
	return model.ManagerAddFileOptions{
		Hash:          "oldhash",
		Name:          "selection",
		FileSelection: []model.TorrentFile{{Path: "R1/a", Wanted: true}, {Path: "R1/b", Wanted: false}},
	}
}

func unwantedSets(stub *stubServer) []map[string]interface{} {
	var out []map[string]interface{}
	for _, call := range stub.setCalls {
		if _, ok := call["files-unwanted"]; ok {
			out = append(out, call)
		}
	}
	return out
}

func idsAre(v interface{}, hash string) bool {
	ids, _ := v.([]interface{})
	return len(ids) == 1 && strings.EqualFold(ids[0].(string), hash)
}

func TestAddFromBuffer_SelectionAddsPausedSetsUnwantedThenStarts(t *testing.T) {
	stub, c, data, hash := newSelectionStub(t)

	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), selectionOpts()); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if len(stub.addCalls) != 1 || stub.addCalls[0]["paused"] != true {
		t.Fatalf("expected one paused torrent-add, got %v", stub.addCalls)
	}
	sets := unwantedSets(stub)
	if len(sets) != 1 || !idsAre(sets[0]["ids"], hash) {
		t.Fatalf("expected one files-unwanted torrent-set for %s, got %v", hash, stub.setCalls)
	}
	idx, _ := sets[0]["files-unwanted"].([]interface{})
	if len(idx) != 2 || idx[0] != float64(1) || idx[1] != float64(2) {
		t.Errorf("files-unwanted: got %v, want [1 2]", sets[0]["files-unwanted"])
	}
	if len(stub.startCalls) != 1 || !idsAre(stub.startCalls[0]["ids"], hash) {
		t.Fatalf("expected one torrent-start for %s, got %v", hash, stub.startCalls)
	}
	setAt, startAt := -1, -1
	for i, m := range stub.methods {
		switch m {
		case "torrent-set":
			setAt = i
		case "torrent-start":
			startAt = i
		}
	}
	if setAt < 0 || startAt < setAt {
		t.Errorf("expected torrent-start after torrent-set, methods %v", stub.methods)
	}
}

func TestAddFromBuffer_SelectionStoppedSkipsStart(t *testing.T) {
	stub, c, data, _ := newSelectionStub(t)
	opts := selectionOpts()
	opts.Stopped = true

	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if len(stub.addCalls) != 1 || stub.addCalls[0]["paused"] != true {
		t.Fatalf("expected one paused torrent-add, got %v", stub.addCalls)
	}
	if len(unwantedSets(stub)) != 1 {
		t.Errorf("expected one files-unwanted torrent-set, got %v", stub.setCalls)
	}
	if len(stub.startCalls) != 0 {
		t.Errorf("expected no torrent-start for a stopped add, got %v", stub.startCalls)
	}
}

func TestAddFromBuffer_SelectionAllUnwantedSetsAllThenStarts(t *testing.T) {
	stub, c, data, hash := newSelectionStub(t)
	opts := selectionOpts()
	opts.FileSelection = []model.TorrentFile{{Path: "R1/a", Wanted: false}, {Path: "R1/b", Wanted: false}}

	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	sets := unwantedSets(stub)
	if len(sets) != 1 || !idsAre(sets[0]["ids"], hash) {
		t.Fatalf("expected one files-unwanted torrent-set for %s, got %v", hash, stub.setCalls)
	}
	if got, _ := sets[0]["files-unwanted"].([]interface{}); !slices.Equal(got, []interface{}{float64(0), float64(1), float64(2)}) {
		t.Errorf("files-unwanted: got %v, want [0 1 2]", sets[0]["files-unwanted"])
	}
	if len(stub.startCalls) != 1 || !idsAre(stub.startCalls[0]["ids"], hash) {
		t.Fatalf("expected one torrent-start for %s, got %v", hash, stub.startCalls)
	}
	if setAt, startAt := slices.Index(stub.methods, "torrent-set"), slices.Index(stub.methods, "torrent-start"); setAt < 0 || startAt < setAt {
		t.Errorf("expected torrent-start after torrent-set, methods %v", stub.methods)
	}
	if len(stub.removeCalls) != 0 {
		t.Errorf("expected no torrent-remove, got %v", stub.removeCalls)
	}
}

func TestAddFromBuffer_SelectionNothingUnwantedSendsNoSet(t *testing.T) {
	stub, c, data, hash := newSelectionStub(t)
	stub.addFiles = []rpcFile{{Name: "R2/a"}}
	stub.addFileStats = []rpcFileStat{{Wanted: true}}

	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), selectionOpts()); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if sets := unwantedSets(stub); len(sets) != 0 {
		t.Errorf("expected no files-unwanted torrent-set, got %v", sets)
	}
	if len(stub.startCalls) != 1 || !idsAre(stub.startCalls[0]["ids"], hash) {
		t.Errorf("expected one torrent-start for %s, got %v", hash, stub.startCalls)
	}
}

func TestAddFromBuffer_SelectionSetFailureDiscards(t *testing.T) {
	stub, c, data, hash := newSelectionStub(t)
	stub.failSetUnwanted = true

	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), selectionOpts())
	if err == nil {
		t.Fatal("expected an error when files-unwanted fails")
	}
	if len(stub.removeCalls) != 1 || !idsAre(stub.removeCalls[0]["ids"], hash) || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected one torrent-remove of %s with delete-local-data=false, got %v", hash, stub.removeCalls)
	}
	if len(stub.startCalls) != 0 {
		t.Errorf("expected no torrent-start, got %v", stub.startCalls)
	}
}

func TestAddFromBuffer_NoSelectionNoExtraCalls(t *testing.T) {
	stub, c, data, _ := newSelectionStub(t)
	opts := selectionOpts()
	opts.FileSelection = nil

	if err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts); err != nil {
		t.Fatalf("AddFromBuffer: %v", err)
	}
	if len(stub.addCalls) != 1 {
		t.Fatalf("expected 1 add call, got %d", len(stub.addCalls))
	}
	if _, ok := stub.addCalls[0]["paused"]; ok {
		t.Errorf("expected no paused key, got %v", stub.addCalls[0]["paused"])
	}
	if stub.fileGets != 0 || len(stub.startCalls) != 0 || len(stub.setCalls) != 0 {
		t.Errorf("expected no file fields, set or start, got fileGets %d sets %v starts %v", stub.fileGets, stub.setCalls, stub.startCalls)
	}
	want := []string{"session-get", "torrent-get", "torrent-add", "torrent-get"}
	if strings.Join(stub.methods, ",") != strings.Join(want, ",") {
		t.Errorf("methods: got %v, want %v", stub.methods, want)
	}
}

func TestAddFromBuffer_SelectionEmptyFilesDiscards(t *testing.T) {
	stub, c, data, hash := newSelectionStub(t)
	stub.addFiles = nil
	stub.addFileStats = nil

	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), selectionOpts())
	if err == nil {
		t.Fatal("expected an error when the new torrent reports no files")
	}
	if len(stub.removeCalls) != 1 || !idsAre(stub.removeCalls[0]["ids"], hash) || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected one torrent-remove of %s with delete-local-data=false, got %v", hash, stub.removeCalls)
	}
	if len(stub.startCalls) != 0 {
		t.Errorf("expected no torrent-start, got %v", stub.startCalls)
	}
}

func TestMapEntry_Unhealthy(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})
	cases := []struct {
		status, errCode int
		want            bool
	}{
		{status: 6, want: false},
		{status: 0, want: false},
		{status: 4, errCode: 2, want: false},
		{status: 0, errCode: 3, want: true},
		{status: 1, want: true},
		{status: 2, want: true},
	}
	for _, cse := range cases {
		rt := &rpcTorrent{DownloadDir: "/downloads", Status: cse.status, Error: cse.errCode}
		if got := c.mapEntry(rt).Unhealthy; got != cse.want {
			t.Errorf("status=%d error=%d: Unhealthy = %v, want %v", cse.status, cse.errCode, got, cse.want)
		}
	}
}

func TestListRequestsErrorField(t *testing.T) {
	if !slices.Contains(torrentFields, "error") {
		t.Errorf("torrentFields %v lack error, List cannot see a local error", torrentFields)
	}
}

func TestDelete_CallerKeepFilesWins(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	stub.putTorrent(rpcTorrent{HashString: "h1", DownloadDir: "/downloads/movies"})
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "movies", Name: "x", DeleteFiles: false}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected delete-local-data=false, got %v", stub.removeCalls)
	}
}

func TestDeletesFilesIgnoresEmptyCategory(t *testing.T) {
	cfg := model.ManagerConfig{DeleteEnabled: true, DeleteFilesEnabled: true, DeleteCategories: []string{"tv", ""}}
	cases := []struct {
		downloadDir string
		category    string
		want        bool
	}{
		{"/downloads", "", false},
		{"/downloads/tv", "tv", true},
	}
	for _, tc := range cases {
		stub := newStubServer(t, "/downloads")
		stub.putTorrent(rpcTorrent{HashString: "h1", DownloadDir: tc.downloadDir})
		c := newTestClient(t, stub, cfg)
		if got := c.DeletesFiles(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: tc.category}); got != tc.want {
			t.Fatalf("DeletesFiles(%q) = %v, want %v", tc.category, got, tc.want)
		}
	}
}
