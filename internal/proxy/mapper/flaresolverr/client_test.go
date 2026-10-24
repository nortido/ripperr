package flaresolverr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendPostAndDecode(t *testing.T) {
	var got Payload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		_, _ = w.Write([]byte(`{"status":"ok","solution":{"status":200,"response":"<html/>","userAgent":"ua"}}`))
	}))
	defer srv.Close()

	c := New(srv.Client(), &Config{Host: srv.URL, MaxTimeout: 60000})
	req, _ := http.NewRequest(http.MethodPost, "https://example.com/login", strings.NewReader("a=1"))
	res, err := c.Send(req, &Payload{URL: req.URL.String(), Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmd != "request.post" || got.PostData != "a=1" || got.MaxTimeout != 60000 || got.Session != "s1" {
		t.Fatalf("payload = %+v", got)
	}
	if res.Status != "ok" || res.Solution.Status != 200 || res.Solution.UserAgent != "ua" {
		t.Fatalf("response = %+v", res)
	}
}

func TestSendStopsOnRequestContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	c := New(srv.Client(), &Config{Host: srv.URL, MaxTimeout: 60000})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/", nil)

	done := make(chan error, 1)
	go func() {
		_, err := c.Send(req, &Payload{URL: req.URL.String()})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send ignored the request context")
	}
}
