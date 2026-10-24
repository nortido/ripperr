package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	qbt "github.com/autobrr/go-qbittorrent"

	"ripperr/internal/helper/torrentmeta"
	"ripperr/internal/model"
)

func TestIncomingHashSameAs_NilBufferWithMagnet(t *testing.T) {
	// manager.SubmitMagnet зовет IncomingHashSameAs с buf == nil на каждом магните.
	// Без проверки на nil это паника на горячем пути
	c := &Client{}
	hash := "0123456789abcdef0123456789abcdef01234567"
	magnet := "magnet:?xt=urn:btih:" + hash

	same, known := c.IncomingHashSameAs(nil, magnet, hash)
	if !same || !known {
		t.Errorf("got same=%v known=%v, want same=true known=true", same, known)
	}
}

func TestIncomingHashSameAs_NilBufferNoMagnet(t *testing.T) {
	c := &Client{}
	hash := "0123456789abcdef0123456789abcdef01234567"

	same, known := c.IncomingHashSameAs(nil, "", hash)
	if same || known {
		t.Errorf("got same=%v known=%v, want same=false known=false", same, known)
	}
}

func buildHybridTestTorrent(t *testing.T) ([]byte, string) {
	t.Helper()
	info := metainfo.Info{
		PieceLength: 16384,
		Pieces:      make([]byte, 20),
		Name:        "test.txt",
		Length:      1,
		MetaVersion: 2,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes, Announce: "http://tracker.example/announce"}
	var buf bytes.Buffer
	if err = mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	return buf.Bytes(), mi.HashInfoBytes().HexString()
}

func newAddCounter(t *testing.T) (*Client, *atomic.Int32) {
	t.Helper()
	var adds atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/webapiVersion", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("4.6.0"))
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]qbt.Torrent{})
	})
	mux.HandleFunc("/api/v2/torrents/add", func(w http.ResponseWriter, _ *http.Request) {
		adds.Add(1)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	c, err := New(model.ManagerConfig{Host: server.URL, DeleteEnabled: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, &adds
}

func TestAddRefusesV2Torrent(t *testing.T) {
	c, adds := newAddCounter(t)
	data, hash := buildHybridTestTorrent(t)

	err := c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: hash, Name: "x"})
	if err == nil || !strings.Contains(err.Error(), "v2 metadata") {
		t.Fatalf("err = %v, want a v2 metadata refusal", err)
	}
	if n := adds.Load(); n != 0 {
		t.Errorf("add requests = %d, want 0", n)
	}
}

func TestAddRefusesV2Magnet(t *testing.T) {
	c, adds := newAddCounter(t)
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567" +
		"&xt=urn:btmh:12200123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef&dn=x"

	err := c.AddFromMagnet(context.Background(), magnet, model.ManagerAddFileOptions{Name: "x"})
	if err == nil || !strings.Contains(err.Error(), "v2 metadata") {
		t.Fatalf("err = %v, want a v2 metadata refusal", err)
	}
	if n := adds.Load(); n != 0 {
		t.Errorf("add requests = %d, want 0", n)
	}
}

func TestAddKeepsV1TorrentWithOddInfo(t *testing.T) {
	data := oddInfoTorrent()
	hash, err := torrentmeta.InfoHash(data)
	if err != nil {
		t.Fatalf("InfoHash: %v", err)
	}
	if _, err := torrentmeta.HasV2(data); err == nil {
		t.Fatal("HasV2 accepts the odd info, test torrent no longer covers the error path")
	}
	c, adds := newAddCounter(t)

	_ = c.AddFromBuffer(context.Background(), bytes.NewBuffer(data), model.ManagerAddFileOptions{Hash: hash, Name: "x"})
	if n := adds.Load(); n != 1 {
		t.Errorf("add requests = %d, want 1: a v1 torrent that added before must still add", n)
	}
}

func oddInfoTorrent() []byte {
	// name числом: InfoHash такой файл принимает, а UnmarshalInfo нет
	return []byte("d8:announce31:http://tracker.example/announce4:infod6:lengthi1e4:namei5e12:piece lengthi16384e6:pieces20:" +
		strings.Repeat("a", 20) + "ee")
}
