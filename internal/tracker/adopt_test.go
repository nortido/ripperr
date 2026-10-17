package tracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"ripperr/internal/helper"
	trackerModel "ripperr/internal/model/tracker"
	"ripperr/internal/tracker/client"
	"ripperr/internal/tracker/client/aniliberty"
)

type adoptClient struct {
	client.IClient
	mu       sync.Mutex
	calls    []string
	unmarked int
	fail     bool
	err      error
	body     string
}

func (c *adoptClient) GetResponse(ctx context.Context, link string, _ bool) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, link)
	if !client.NotFoundAnswer(ctx) {
		c.unmarked++
	}
	if c.err != nil {
		return nil, c.err
	}
	if c.fail {
		return nil, errors.New("api down")
	}
	body := `{"release":{"id":1,"alias":"some-alias"}}`
	if c.body != "" {
		body = c.body
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewBufferString(body))}, nil
}

const (
	adoptTestName = "Cyberpunk Edgerunners 2 - AniLiberty [WEB-DL 1080p HEVC]"
	adoptTestHash = "a5161b462482f3848fd5889733026ee11d4bc680"
	adoptTestURL  = "https://aniliberty.top/anime/releases/release/some-alias"
)

func newAdoptService(enabled bool) (*service, *adoptClient) {
	tr := aniliberty.New(trackerModel.Tracker{
		Name:        "aniliberty",
		Domain:      "aniliberty.top",
		DownloadURL: "https://aniliberty.top/api/v1/anime/torrents/",
		Enabled:     true,
	})
	ac := &adoptClient{IClient: client.New(nil, nil, tr, nil, helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{}))}
	s := &service{
		handlerRegistry:    map[int]client.ITracker{},
		clientIndex:        map[int]*CachedTracker{15: {IClient: ac, Created: time.Now()}},
		flareTimeoutCounts: make(map[int]int),
		staleUseCounts:     make(map[int]int),
	}
	if enabled {
		s.handlerRegistry[15] = tr
	}
	return s, ac
}

func TestAdoptMemoizes(t *testing.T) {
	s, ac := newAdoptService(true)
	for i := 0; i < 2; i++ {
		got, err := s.Adopt(context.Background(), adoptTestName, adoptTestHash)
		if err != nil || got != adoptTestURL {
			t.Fatalf("Adopt #%d = %q, %v, want %q", i, got, err, adoptTestURL)
		}
	}
	if len(ac.calls) != 1 {
		t.Fatalf("requests = %d (%v), want 1", len(ac.calls), ac.calls)
	}
}

func TestAdoptFailureNotMemoized(t *testing.T) {
	s, ac := newAdoptService(true)
	ac.fail = true
	if got, err := s.Adopt(context.Background(), adoptTestName, adoptTestHash); err == nil || got != "" {
		t.Fatalf("Adopt = %q, %v, want an error", got, err)
	}
	ac.fail = false
	got, err := s.Adopt(context.Background(), adoptTestName, adoptTestHash)
	if err != nil || got != adoptTestURL {
		t.Fatalf("Adopt after failure = %q, %v, want %q", got, err, adoptTestURL)
	}
	if len(ac.calls) != 2 {
		t.Fatalf("requests = %d, want 2 (the failure must not be cached)", len(ac.calls))
	}
}

func TestAdoptSkipsForeignAndDisabled(t *testing.T) {
	s, ac := newAdoptService(true)
	if got, err := s.Adopt(context.Background(), "Some Other Release", adoptTestHash); err != nil || got != "" {
		t.Fatalf("Adopt foreign name = %q, %v, want empty", got, err)
	}
	s, ac2 := newAdoptService(false)
	if got, err := s.Adopt(context.Background(), adoptTestName, adoptTestHash); err != nil || got != "" {
		t.Fatalf("Adopt with the tracker disabled = %q, %v, want empty", got, err)
	}
	if len(ac.calls)+len(ac2.calls) != 0 {
		t.Fatalf("requests = %v %v, want none", ac.calls, ac2.calls)
	}
}

func TestAdoptDefinitiveMissMemoized(t *testing.T) {
	cases := map[string]func(c *adoptClient){
		"404": func(c *adoptClient) {
			c.err = fmt.Errorf("aniliberty error: %w", &client.StatusError{Code: 404, Msg: "aniliberty: unexpected status code 404 (type=application/json, bytes=40)"})
		},
		"no alias": func(c *adoptClient) { c.body = `{"release":{"id":1}}` },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			s, ac := newAdoptService(true)
			setup(ac)
			for i := 0; i < 2; i++ {
				if got, err := s.Adopt(context.Background(), adoptTestName, adoptTestHash); err != nil || got != "" {
					t.Fatalf("Adopt #%d = %q, %v, want empty and no error", i, got, err)
				}
			}
			if ac.unmarked != 0 {
				t.Fatalf("%d lookups without the not-found marker, want 0", ac.unmarked)
			}
			if len(ac.calls) != 1 {
				t.Fatalf("requests = %d, want 1 (a definitive miss is memoized)", len(ac.calls))
			}
		})
	}
}

func TestAdoptServerErrorRetried(t *testing.T) {
	s, ac := newAdoptService(true)
	ac.err = errors.New("aniliberty: unexpected status code 502 (type=text/html, bytes=10)")
	for i := 0; i < 2; i++ {
		if _, err := s.Adopt(context.Background(), adoptTestName, adoptTestHash); err == nil {
			t.Fatalf("Adopt #%d: want an error on a 5xx", i)
		}
	}
	if len(ac.calls) != 2 {
		t.Fatalf("requests = %d, want 2 (a transient failure is retried)", len(ac.calls))
	}
}
