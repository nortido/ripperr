package transmission

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

// stubServer это минимальный поддельный Transmission: держит торренты в памяти
// и обслуживает ровно те методы RPC, которые использует Client.
type stubServer struct {
	mu          sync.Mutex
	downloadDir string
	torrents    map[string]rpcTorrent
	addCalls    []map[string]interface{}
	removeCalls []map[string]interface{}
	setCalls    []map[string]interface{}
	// failEnsurePresent: не добавлять торрент по-настоящему, чтобы
	// смоделировать молчаливый отказ Transmission
	failEnsurePresent bool
	// duplicateFor: torrent-add с этим хешем отвечает torrent-duplicate вместо
	// torrent-added, как настоящий Transmission на повторное добавление, и не
	// трогает состояние торрентов
	duplicateFor string
	// failTorrentGetFor: torrent-get с этим хешем в ids отвечает ошибкой,
	// моделирует сбой сети при проверке categoryInsideBase перед удалением
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
		switch req.Method {
		case "session-get":
			writeResult(w, map[string]interface{}{"download-dir": s.downloadDir})
		case "torrent-get":
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
			s.setCalls = append(s.setCalls, req.Arguments)
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

// категория из downloadDir

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
		// два уровня внутри базы: категория это только первый сегмент (tv), а не
		// весь хвост (tv/show1), иначе у каждого торрента была бы своя категория
		{"two levels inside", "/downloads/tv/show1", "/downloads", "tv", true},
		{"outside base", "/other/place", "/downloads", "place", false},
		// одноимённый каталог вне базы: категория совпадает по basename с той, что
		// была бы внутри базы, но insideBase обязан различать их для Delete
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

	// базовый каталог сессии пуст (session-get ничего не отдал): результат
	// никогда не должен быть относительным путём
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
	// status stopped (0) + percentDone >= 1 -> paused max ratio overrides seedRatioMode
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
	// session-get у стаба отдаёт пустой download-dir, как настоящий Transmission
	// может при неожиданном ответе
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

	// opts.Hash умышленно другой: та же ветка, что и "чужой торрент уже занял инфохеш"
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
	// Transmission на дубликат шлёт result:"success" с torrent-duplicate вместо
	// torrent-added; без разбора этого ключа ensurePresentAfterAdd видит старый
	// торрент и молча рапортует успех
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
	// раздача реально лежит внутри базового каталога: категория "movies" совпадает
	// и с DeleteCategories, и с реальным downloadDir, удаление файлов разрешено
	stub.putTorrent(rpcTorrent{HashString: "h1", DownloadDir: "/downloads/movies"})
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "movies", Name: "x"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != true {
		t.Fatalf("expected delete-local-data=true, got %v", stub.removeCalls)
	}
}

func TestDelete_CategoryMatchesButOutsideBaseKeepsFiles(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	// /mnt/archive/movies даёт ту же категорию "movies" по basename, что и
	// /downloads/movies, но лежит вне базового каталога: пользователь, добавивший
	// "movies" в DeleteFilesCategories, имел в виду каталог внутри базы, поэтому
	// файлы снаружи неё удалять нельзя
	stub.putTorrent(rpcTorrent{HashString: "h1", DownloadDir: "/mnt/archive/movies"})
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "movies", Name: "x"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected delete-local-data=false for a category outside the base dir, got %v", stub.removeCalls)
	}
}

func TestDelete_CategoryNotInListKeepsFiles(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "tv", Name: "x"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected delete-local-data=false, got %v", stub.removeCalls)
	}
}

func TestDelete_CategoryInsideBaseCheckFailsKeepsFiles(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	// сеть/клиент отказывают именно на проверке downloadDir перед удалением:
	// categoryInsideBase обязан трактовать это как "не внутри" и не удалять файлы,
	// о принадлежности которых ничего не удалось узнать
	stub.failTorrentGetFor = "h1"
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "movies", Name: "x"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected delete-local-data=false when the downloadDir check fails, got %v", stub.removeCalls)
	}
}

func TestDelete_TorrentAbsentFromCheckKeepsFiles(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	// категория совпадает с DeleteCategories, но самой раздачи в клиенте уже нет
	// (torrentByHash возвращает nil, а не ошибку): тот же безопасный дефолт
	c := newTestClient(t, stub, model.ManagerConfig{DeleteEnabled: true, DeleteCategories: []string{"movies"}, DeleteFilesEnabled: true})

	if err := c.Delete(context.Background(), model.ManagerDeleteOptions{Hash: "h1", Category: "movies", Name: "x"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(stub.removeCalls) != 1 || stub.removeCalls[0]["delete-local-data"] != false {
		t.Fatalf("expected delete-local-data=false when the torrent is absent, got %v", stub.removeCalls)
	}
}

func TestForceRedownload_Unsupported(t *testing.T) {
	stub := newStubServer(t, "/downloads")
	c := newTestClient(t, stub, model.ManagerConfig{})

	if err := c.ForceRedownload(context.Background(), "somehash"); !errors.Is(err, ErrRedownloadUnsupported) {
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
	// manager.SubmitMagnet вызывает IncomingHashSameAs с buf == nil на каждом
	// магнитном сабмите: без guard на nil это паника на горячем пути
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
