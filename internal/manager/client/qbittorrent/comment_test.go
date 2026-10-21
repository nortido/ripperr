package qbittorrent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	qbt "github.com/autobrr/go-qbittorrent"

	"ripperr/internal/model"
)

type commentServer struct {
	mu      sync.Mutex
	status  int
	stored  string
	persist bool
	method  string
	form    map[string]string
}

func newCommentClient(t *testing.T, cs *commentServer) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/torrents/setComment", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		cs.mu.Lock()
		defer cs.mu.Unlock()
		cs.method = r.Method
		cs.form = map[string]string{"hashes": r.PostForm.Get("hashes"), "comment": r.PostForm.Get("comment")}
		if cs.status != http.StatusOK {
			w.WriteHeader(cs.status)
			return
		}
		if cs.persist {
			cs.stored = r.PostForm.Get("comment")
		}
	})
	mux.HandleFunc("/api/v2/torrents/info", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]qbt.Torrent{{Hash: "abc", Name: "movie"}})
	})
	mux.HandleFunc("/api/v2/torrents/properties", func(w http.ResponseWriter, _ *http.Request) {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		_ = json.NewEncoder(w).Encode(qbt.TorrentProperties{Comment: cs.stored})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(model.ManagerConfig{Host: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestSetCommentPostsForm(t *testing.T) {
	const link = "https://aniliberty.top/anime/releases/release/x"
	cs := &commentServer{status: http.StatusOK, persist: true}
	c := newCommentClient(t, cs)
	if err := c.SetComment(context.Background(), "abc", link); err != nil {
		t.Fatalf("SetComment: %v", err)
	}
	if cs.method != http.MethodPost {
		t.Errorf("method = %q, want POST", cs.method)
	}
	if cs.form["hashes"] != "abc" || cs.form["comment"] != link {
		t.Errorf("form = %v", cs.form)
	}
}

func TestSetCommentUnsupported(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		c := newCommentClient(t, &commentServer{status: status})
		err := c.SetComment(context.Background(), "abc", "https://x/1")
		if !errors.Is(err, model.ErrCommentUnsupported) {
			t.Errorf("status %d: err = %v, want ErrCommentUnsupported", status, err)
		}
	}
}

func TestSetCommentSilentSkip(t *testing.T) {
	c := newCommentClient(t, &commentServer{status: http.StatusOK})
	err := c.SetComment(context.Background(), "abc", "https://x/1")
	if err == nil || errors.Is(err, model.ErrCommentUnsupported) {
		t.Fatalf("err = %v, want a plain error when the comment did not stick", err)
	}
}

func TestSetCommentBadRequestIsPlainError(t *testing.T) {
	c := newCommentClient(t, &commentServer{status: http.StatusBadRequest})
	err := c.SetComment(context.Background(), "abc", "https://x/1")
	if err == nil || errors.Is(err, model.ErrCommentUnsupported) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}
