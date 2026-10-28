package qbittorrent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"ripperr/internal/model"
)

type freeSpaceServer struct {
	mu          sync.Mutex
	status      int
	body        string
	gotPath     string
	maindataHit int
}

func newFreeSpaceClient(t *testing.T, fs *freeSpaceServer) *Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/app/getFreeSpaceAtPath", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		fs.mu.Lock()
		defer fs.mu.Unlock()
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		fs.gotPath = r.PostForm.Get("path")
		w.WriteHeader(fs.status)
		_, _ = w.Write([]byte(fs.body))
	})
	mux.HandleFunc("/api/v2/app/defaultSavePath", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("/downloads"))
	})
	mux.HandleFunc("/api/v2/sync/maindata", func(w http.ResponseWriter, _ *http.Request) {
		fs.mu.Lock()
		fs.maindataHit++
		fs.mu.Unlock()
		_, _ = w.Write([]byte(`{"rid":1,"server_state":{"free_space_on_disk":777}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(model.ManagerConfig{Host: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestFreeSpaceAtPath(t *testing.T) {
	fs := &freeSpaceServer{status: http.StatusOK, body: "123456789\n"}
	c := newFreeSpaceClient(t, fs)
	got, err := c.FreeSpace(context.Background(), "/downloads/tv")
	if err != nil {
		t.Fatalf("FreeSpace: %v", err)
	}
	if got != 123456789 {
		t.Errorf("free = %d, want 123456789", got)
	}
	if fs.gotPath != "/downloads/tv" {
		t.Errorf("path form = %q", fs.gotPath)
	}
}

func TestFreeSpaceZeroIsValue(t *testing.T) {
	c := newFreeSpaceClient(t, &freeSpaceServer{status: http.StatusOK, body: `"0"`})
	got, err := c.FreeSpace(context.Background(), "/downloads")
	if err != nil || got != 0 {
		t.Fatalf("FreeSpace = %d, %v; want 0, nil", got, err)
	}
}

func TestFreeSpaceNegative(t *testing.T) {
	c := newFreeSpaceClient(t, &freeSpaceServer{status: http.StatusOK, body: "-1"})
	_, err := c.FreeSpace(context.Background(), "/downloads")
	if !errors.Is(err, model.ErrFreeSpaceUnknown) {
		t.Fatalf("err = %v, want ErrFreeSpaceUnknown", err)
	}
}

func TestFreeSpaceOtherStatusIsError(t *testing.T) {
	c := newFreeSpaceClient(t, &freeSpaceServer{status: http.StatusInternalServerError})
	_, err := c.FreeSpace(context.Background(), "/downloads")
	if err == nil || errors.Is(err, model.ErrFreeSpaceUnknown) {
		t.Fatalf("err = %v, want plain error", err)
	}
}

func TestFreeSpaceFallbackDefaultPath(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed} {
		for _, path := range []string{"/downloads", "/downloads/tv/", "/downloads\\"} {
			fs := &freeSpaceServer{status: status}
			c := newFreeSpaceClient(t, fs)
			got, err := c.FreeSpace(context.Background(), path)
			if err != nil || got != 777 {
				t.Errorf("status %d path %q: %d, %v; want 777, nil", status, path, got, err)
			}
		}
	}
	for _, path := range []string{"/downloadsX", "/mnt/x"} {
		fs := &freeSpaceServer{status: http.StatusNotFound}
		c := newFreeSpaceClient(t, fs)
		_, err := c.FreeSpace(context.Background(), path)
		if !errors.Is(err, model.ErrFreeSpaceUnknown) {
			t.Errorf("path %q: err = %v, want ErrFreeSpaceUnknown", path, err)
		}
		if fs.maindataHit != 0 {
			t.Errorf("path %q: maindata requested %d times", path, fs.maindataHit)
		}
	}
}

func TestFreeSpaceOversizedBodyIsError(t *testing.T) {
	c := newFreeSpaceClient(t, &freeSpaceServer{status: http.StatusOK, body: strings.Repeat("1", 2048)})
	_, err := c.FreeSpace(context.Background(), "/downloads")
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want too large body error", err)
	}
}
