package transmission

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRPCURL_NoScheme(t *testing.T) {
	if got := rpcURL("192.168.1.1:9091"); got != "http://192.168.1.1:9091/transmission/rpc" {
		t.Errorf("got %q", got)
	}
}

func TestRPCURL_WithSchemeAndTrailingSlash(t *testing.T) {
	if got := rpcURL("https://tr.example.com/"); got != "https://tr.example.com/transmission/rpc" {
		t.Errorf("got %q", got)
	}
}

func TestRPCCall_RetriesOnceOn409(t *testing.T) {
	var requests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			w.Header().Set(sessionHeader, "abc123")
			w.WriteHeader(http.StatusConflict)
			return
		}
		if r.Header.Get(sessionHeader) != "abc123" {
			t.Errorf("second request missing session id header, got %q", r.Header.Get(sessionHeader))
		}
		_ = json.NewEncoder(w).Encode(rpcResponse{Result: "success"})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c := newRPCClient(server.URL, "", "")
	if _, err := c.call(context.Background(), "session-get", nil); err != nil {
		t.Fatalf("call: %v", err)
	}
	if requests.Load() != 2 {
		t.Errorf("expected exactly 2 requests (1 retry), got %d", requests.Load())
	}
}

func TestRPCCall_BasicAuth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "alice" || pass != "secret" {
			t.Errorf("expected basic auth alice/secret, got ok=%v user=%q pass=%q", ok, user, pass)
		}
		_ = json.NewEncoder(w).Encode(rpcResponse{Result: "success"})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c := newRPCClient(server.URL, "alice", "secret")
	if _, err := c.call(context.Background(), "session-get", nil); err != nil {
		t.Fatalf("call: %v", err)
	}
}

func TestRPCCall_ErrorOnNonSuccessResult(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rpcResponse{Result: "duplicate torrent"})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c := newRPCClient(server.URL, "", "")
	if _, err := c.call(context.Background(), "torrent-add", nil); err == nil {
		t.Fatal("expected error for non-success result")
	}
}

func TestRPCCall_ErrorOnBadHTTPStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/transmission/rpc", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	c := newRPCClient(server.URL, "", "")
	if _, err := c.call(context.Background(), "session-get", nil); err == nil {
		t.Fatal("expected error for 500 status")
	}
}
