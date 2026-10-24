package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"ripperr/internal/model/proxy"
)

func TestDestroyFlareSessionHonorsContext(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	px := New(srv.Client(), &proxy.Config{Flaresolverr: proxy.ItemConfig{URL: srv.URL}})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- px.DestroyFlareSession(ctx, "s1") }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DestroyFlareSession ignored ctx")
	}
}

type sessionProbe struct {
	mu      sync.Mutex
	cur     map[string]int
	max     int
	gets    int
	started chan int
	release chan struct{}
	hold    time.Duration
}

func newSessionProbe(hold time.Duration) *sessionProbe {
	return &sessionProbe{cur: map[string]int{}, started: make(chan int, 16), release: make(chan struct{}), hold: hold}
}

func (p *sessionProbe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var pl struct {
		Cmd     string `json:"cmd"`
		Session string `json:"session"`
	}
	_ = json.NewDecoder(r.Body).Decode(&pl)
	w.Header().Set("Content-Type", "application/json")
	if pl.Cmd == cmdDestroySession {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
		return
	}
	p.mu.Lock()
	p.gets++
	n := p.gets
	p.cur[pl.Session]++
	p.max = max(p.max, p.cur[pl.Session])
	p.mu.Unlock()
	p.started <- n
	if n == 1 {
		<-p.release
	} else {
		time.Sleep(p.hold)
	}
	p.mu.Lock()
	p.cur[pl.Session]--
	p.mu.Unlock()
	_, _ = w.Write([]byte(`{"status":"ok","solution":{"status":200,"response":"x"}}`))
}

func newProbeProxy(t *testing.T, p *sessionProbe) IClient {
	t.Helper()
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-p.release:
		default:
			close(p.release)
		}
	})
	return New(srv.Client(), &proxy.Config{Flaresolverr: proxy.ItemConfig{URL: srv.URL}})
}

func sendOnce(ctx context.Context, px IClient, session string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://rutracker.org/", nil)
	if err != nil {
		return err
	}
	res, err := px.Send(req, &proxy.Options{Session: session}, proxy.TypeFlare)
	if err != nil {
		return err
	}
	return res.Body.Close()
}

func waitStarted(t *testing.T, p *sessionProbe, want int) {
	t.Helper()
	select {
	case n := <-p.started:
		if n != want {
			t.Fatalf("request #%d started, want #%d", n, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("request #%d never reached the server", want)
	}
}

func TestSessionGateNoTwoCommandsAfterDestroy(t *testing.T) {
	p := newSessionProbe(300 * time.Millisecond)
	px := newProbeProxy(t, p)
	bg := context.Background()
	errs := make(chan error, 3)
	destroyed := make(chan error, 1)

	go func() { errs <- sendOnce(bg, px, "gate1") }()
	waitStarted(t, p, 1)
	go func() { destroyed <- px.DestroyFlareSession(bg, "gate1") }()
	time.Sleep(50 * time.Millisecond)
	go func() { errs <- sendOnce(bg, px, "gate1") }()
	time.Sleep(50 * time.Millisecond)
	close(p.release)
	if err := <-destroyed; err != nil {
		t.Fatalf("destroy: %v", err)
	}
	waitStarted(t, p, 2)
	go func() { errs <- sendOnce(bg, px, "gate1") }()
	for range 3 {
		if err := <-errs; err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.max != 1 {
		t.Fatalf("max concurrent commands for one session = %d, want 1", p.max)
	}
}

func TestDestroyWaitsForLockWithContext(t *testing.T) {
	p := newSessionProbe(0)
	px := newProbeProxy(t, p)
	go func() { _ = sendOnce(context.Background(), px, "gate2") }()
	waitStarted(t, p, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- px.DestroyFlareSession(ctx, "gate2") }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Destroy ignored ctx while waiting for the session lock")
	}
}

func TestSendWaitHonorsRequestContext(t *testing.T) {
	p := newSessionProbe(0)
	px := newProbeProxy(t, p)
	go func() { _ = sendOnce(context.Background(), px, "gate3") }()
	waitStarted(t, p, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sendOnce(ctx, px, "gate3") }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want DeadlineExceeded", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Send ignored request ctx while waiting for the session lock")
	}
}

func TestSessionGateMapStaysBounded(t *testing.T) {
	var g sessionGate
	unlock, err := g.lock(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := g.lock(ctx, "a"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.m) != 0 {
		t.Fatalf("gate map holds %d entries after all unlocks", len(g.m))
	}
}
