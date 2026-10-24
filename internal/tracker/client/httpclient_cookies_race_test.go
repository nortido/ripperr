package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ripperr/internal/helper"
	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/model/tracker"
)

type cookieFlareProxy struct{ n atomic.Int64 }

func (p *cookieFlareProxy) Send(r *http.Request, _ *proxyModel.Options, _ proxyModel.ProxyType) (*http.Response, error) {
	h := http.Header{}
	h.Add("Set-Cookie", fmt.Sprintf("c%d=v", p.n.Add(1)%5))
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Request: r, Header: h}, nil
}

func (p *cookieFlareProxy) DestroyFlareSession(context.Context, string) error { return nil }

func TestConcurrentRequestsAndCookieReads(t *testing.T) {
	tr := &tracker.Tracker{Name: "t", Domain: "example.com", ProxyType: proxyModel.TypeFlare}
	c := New(&http.Client{}, nil, tr, &cookieFlareProxy{}, helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{MaxFailures: 100})).(*client)
	c.header = &http.Header{}

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 50 {
				res, err := c.GetResponse(t.Context(), "https://example.com/a", false)
				if err != nil {
					t.Error(err)
					return
				}
				_ = res.Body.Close()
			}
		})
	}
	wg.Go(func() {
		for range 200 {
			for _, ck := range c.Cookies() {
				_ = ck.Name + ck.Value + ck.Domain
			}
		}
	})
	wg.Wait()
	if len(c.Cookies()) == 0 {
		t.Fatal("no cookies absorbed")
	}
}
