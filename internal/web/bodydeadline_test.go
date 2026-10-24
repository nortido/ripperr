package web

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func shortBodyReadTimeout(t *testing.T) time.Duration {
	t.Helper()
	old := bodyReadTimeout
	bodyReadTimeout = 200 * time.Millisecond
	t.Cleanup(func() { bodyReadTimeout = old })
	return bodyReadTimeout
}

func TestBodyDeadlineCutsSlowBody(t *testing.T) {
	shortBodyReadTimeout(t)
	readErr := make(chan error, 1)
	ts := httptest.NewServer(withBodyDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		readErr <- err
	})))
	defer ts.Close()

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := fmt.Fprint(conn, "POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 100\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if _, err := conn.Write([]byte("a")); err != nil {
					return
				}
			}
		}
	}()

	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("slow body read finished without error")
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("slow body read still running after 1.5 s")
	}
}

func TestBodyDeadlineKeepsLongHandlers(t *testing.T) {
	timeout := shortBodyReadTimeout(t)
	ctxErr := make(chan error, 1)
	ts := httptest.NewServer(withBodyDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Errorf("read body: %v", err)
		}
		time.Sleep(3 * timeout)
		ctxErr <- r.Context().Err()
		w.WriteHeader(http.StatusOK)
	})))
	defer ts.Close()

	for _, tc := range []struct {
		name string
		do   func() (*http.Response, error)
	}{
		{"body read to EOF", func() (*http.Response, error) {
			return http.Post(ts.URL, "text/plain", strings.NewReader("hello"))
		}},
		{"bodiless GET", func() (*http.Response, error) { return http.Get(ts.URL) }},
	} {
		resp, err := tc.do()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: code %d, want 200", tc.name, resp.StatusCode)
		}
		if err := <-ctxErr; err != nil {
			t.Errorf("%s: request context after %v: %v, want alive", tc.name, 3*timeout, err)
		}
	}
}
