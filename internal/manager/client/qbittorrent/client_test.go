package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	qbt "github.com/autobrr/go-qbittorrent"

	"ripperr/internal/model"
)

func newStubServer(t *testing.T, torrents []qbt.Torrent, propsHandler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("4.6.0"))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(torrents)
	})
	mux.HandleFunc("/api/v2/torrents/properties", propsHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestList_Success(t *testing.T) {
	torrents := []qbt.Torrent{
		{Hash: "h1", Name: "torrent-1", Category: "movies"},
		{Hash: "h2", Name: "torrent-2", Category: "movies"},
	}
	server := newStubServer(t, torrents, func(w http.ResponseWriter, r *http.Request) {
		hash := r.URL.Query().Get("hash")
		_ = json.NewEncoder(w).Encode(qbt.TorrentProperties{
			Hash:         hash,
			Comment:      "https://example.com/topic/" + strings.TrimPrefix(hash, "h"),
			AdditionDate: 1700000000,
		})
	})

	c, err := New(model.ManagerConfig{Host: server.URL})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	entries, err := c.List(context.Background(), model.FilterOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Hash != "h1" || entries[1].Hash != "h2" {
		t.Errorf("expected entries in request order, got %+v / %+v", entries[0], entries[1])
	}
	if entries[0].TopicID != "1" || entries[1].TopicID != "2" {
		t.Errorf("expected topic IDs extracted from comment, got %q / %q", entries[0].TopicID, entries[1].TopicID)
	}
}

// TestList_SkipsBadTorrent: при провале загрузки свойств одного торрента
// возвращаются успешные записи; битый торрент пропускается, List отдаёт ErrPartialList.
func TestList_SkipsBadTorrent(t *testing.T) {
	torrents := []qbt.Torrent{
		{Hash: "h1", Name: "torrent-1"},
		{Hash: "h2", Name: "torrent-2"},
		{Hash: "h3", Name: "torrent-3"},
	}
	server := newStubServer(t, torrents, func(w http.ResponseWriter, r *http.Request) {
		hash := r.URL.Query().Get("hash")
		if hash == "h2" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(qbt.TorrentProperties{
			Hash:         hash,
			Comment:      "https://example.com/topic/" + strings.TrimPrefix(hash, "h"),
			AdditionDate: 1700000000,
		})
	})

	c, err := New(model.ManagerConfig{Host: server.URL})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	entries, err := c.List(context.Background(), model.FilterOptions{})
	if !errors.Is(err, model.ErrPartialList) {
		t.Fatalf("expected ErrPartialList, got: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e == nil {
			t.Fatal("expected no nil entries")
		}
		if e.Hash == "h2" {
			t.Fatalf("unexpected failed torrent in result: %+v", e)
		}
	}
}

// TestList_ContextCancellationDoesNotHang это регрессионный тест на исходную
// реализацию List(), которая могла зависать и течь горутинами, когда контекст
// вызывающего отменяли на полпути.
func TestList_ContextCancellationDoesNotHang(t *testing.T) {
	unblock := make(chan struct{})
	defer close(unblock)

	torrents := []qbt.Torrent{
		{Hash: "h1", Name: "torrent-1"},
		{Hash: "h2", Name: "torrent-2"},
	}
	server := newStubServer(t, torrents, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-unblock:
		}
	})

	c, err := New(model.ManagerConfig{Host: server.URL})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := c.List(ctx, model.FilterOptions{})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error once the context times out")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("List did not return after context cancellation: possible hang")
	}
}

func buildTestTorrent(t *testing.T) ([]byte, string) {
	t.Helper()
	info := metainfo.Info{
		PieceLength: 16384,
		Pieces:      make([]byte, 20),
		Name:        "test.txt",
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

// TestAddFromBuffer_WaitsForDeleteRace проверяет, что при DeleteEnabled и
// совпадении хеша нового торрента с заменяемым добавление ждёт исчезновения
// старого, а не валит предпроверку сразу.
func TestAddFromBuffer_WaitsForDeleteRace(t *testing.T) {
	data, hash := buildTestTorrent(t)
	var infoCalls atomic.Int32
	var addCalled atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("4.6.0"))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, r *http.Request) {
		n := infoCalls.Add(1)
		if n <= 2 {
			_ = json.NewEncoder(w).Encode([]qbt.Torrent{{Hash: hash, Name: "old"}})
			return
		}
		if addCalled.Load() {
			_ = json.NewEncoder(w).Encode([]qbt.Torrent{{Hash: hash, Name: "new"}})
			return
		}
		_ = json.NewEncoder(w).Encode([]qbt.Torrent{})
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, r *http.Request) {
		addCalled.Store(true)
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c, err := New(model.ManagerConfig{Host: server.URL, DeleteEnabled: true})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	opts := model.ManagerAddFileOptions{Hash: strings.ToUpper(hash), Name: "new"}
	err = c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !addCalled.Load() {
		t.Fatal("expected torrent add API to be called")
	}
	if infoCalls.Load() < 3 {
		t.Fatalf("expected at least 3 info polls (wait for delete), got %d", infoCalls.Load())
	}
}

func TestGetClientNeverProxiesTorrentClient(t *testing.T) {
	// прокси из окружения увёл бы пароль от веб-морды оператору прокси, а сам
	// торрент-клиент стоит рядом с приложением
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:3128")
	srv := newStubServer(t, nil, func(http.ResponseWriter, *http.Request) {})

	c := &Client{config: model.ManagerConfig{Host: srv.URL, Username: "u", Password: "p"}}
	// ошибка логина здесь не важна, транспорт настраивается до неё
	_ = c.getClient()
	tr, ok := c.client.GetHTTPClient().Transport.(*http.Transport)
	if !ok {
		t.Fatalf("want *http.Transport, got %T", c.client.GetHTTPClient().Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("qbit client still resolves a proxy")
	}
}
