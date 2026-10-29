package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"ripperr/internal/model/proxy"
)

type flareLoadProbe struct {
	mu      sync.Mutex
	cur     int
	max     int
	started chan struct{}
	release chan struct{}
}

func (p *flareLoadProbe) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	p.cur++
	p.max = max(p.max, p.cur)
	p.mu.Unlock()
	p.started <- struct{}{}
	<-p.release
	p.mu.Lock()
	p.cur--
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok","solution":{"status":200,"response":"x"}}`))
}

func newLoadProbeProxy(t *testing.T) (*flareLoadProbe, IClient) {
	t.Helper()
	p := &flareLoadProbe{started: make(chan struct{}, 8), release: make(chan struct{})}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-p.release:
		default:
			close(p.release)
		}
	})
	return p, New(srv.Client(), &proxy.Config{Flaresolverr: proxy.ItemConfig{URL: srv.URL}})
}

func TestFlareRequestsGoOneAtATimeAcrossSessions(t *testing.T) {
	p, px := newLoadProbeProxy(t)
	errs := make(chan error, 3)
	for _, s := range []string{"", "rutracker", "kinozal"} {
		go func() { errs <- sendOnce(context.Background(), px, s) }()
	}
	select {
	case <-p.started:
	case <-time.After(2 * time.Second):
		t.Fatal("no request reached FlareSolverr")
	}
	select {
	case <-p.started:
		t.Fatal("second request reached FlareSolverr while the first is running")
	case <-time.After(200 * time.Millisecond):
	}
	close(p.release)
	for range 3 {
		if err := <-errs; err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.max != 1 {
		t.Fatalf("max concurrent FlareSolverr requests = %d, want 1", p.max)
	}
}

func TestFlareSlotWaitHonorsRequestContext(t *testing.T) {
	p, px := newLoadProbeProxy(t)
	go func() { _ = sendOnce(context.Background(), px, "") }()
	select {
	case <-p.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first request never reached FlareSolverr")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sendOnce(ctx, px, "other") }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Send ignored request ctx while waiting for a FlareSolverr slot")
	}
}
