package mirrorsync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ripperr/internal/config"
	"ripperr/internal/helper"
)

func TestSourceURLs(t *testing.T) {
	const p = "Definitions/kinozal.yml"
	for _, ms := range []config.MirrorSource{{}, {Type: "github"}, {Type: "bogus", URL: "https://x.example/{path}"}} {
		got := SourceURLs(ms)
		if len(got) != 1 || expand(got[0], p) != "https://raw.githubusercontent.com/Jackett/Jackett/master/src/Jackett.Common/Definitions/kinozal.yml" {
			t.Fatalf("%+v: got %v", ms, got)
		}
	}

	got := SourceURLs(config.MirrorSource{Type: "jsdelivr", URL: "https://ignored.example/{path}"})
	var hosts []string
	for _, tmpl := range got {
		hosts = append(hosts, strings.SplitN(expand(tmpl, p), "/", 4)[2])
	}
	want := []string{"cdn.jsdelivr.net", "fastly.jsdelivr.net", "gcore.jsdelivr.net", "testingcf.jsdelivr.net"}
	if !slices.Equal(hosts, want) {
		t.Fatalf("hosts = %v, want %v", hosts, want)
	}
	if first := expand(got[0], p); first != "https://cdn.jsdelivr.net/gh/Jackett/Jackett@master/src/Jackett.Common/Definitions/kinozal.yml" {
		t.Fatalf("first = %s", first)
	}

	custom := "https://m.example/x?file={path}&b=1"
	if got := SourceURLs(config.MirrorSource{Type: "custom", URL: custom}); !slices.Equal(got, []string{custom}) {
		t.Fatalf("custom = %v", got)
	}
}

type hostStub struct {
	srv  *httptest.Server
	hits atomic.Int32
}

func newHostStub(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) *hostStub {
	t.Helper()
	s := &hostStub{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		h(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func statusStub(t *testing.T, code int) *hostStub {
	return newHostStub(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
}

func okStub(t *testing.T) *hostStub {
	return newHostStub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(yamlFixture)) })
}

func tmpls(stubs ...*hostStub) []string {
	out := make([]string, len(stubs))
	for i, s := range stubs {
		out[i] = s.srv.URL + "/{path}"
	}
	return out
}

func hitsOf(stubs ...*hostStub) []int32 {
	out := make([]int32, len(stubs))
	for i, s := range stubs {
		out[i] = s.hits.Load()
	}
	return out
}

func TestFetchAnyFallback(t *testing.T) {
	const p = "Definitions/ok.yml"
	forbidden, limited := statusStub(t, http.StatusForbidden), statusStub(t, http.StatusTooManyRequests)
	garbage := newHostStub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hello")) })
	good := okStub(t)
	all := []*hostStub{forbidden, limited, garbage, good}
	ts := tmpls(all...)
	hc := &http.Client{Timeout: 2 * time.Second}

	start := 0
	got, err := fetchAny(context.Background(), hc, ts, &start, p)
	if err != nil || len(got.Links) != 3 || start != 3 {
		t.Fatalf("got %+v err %v start %d", got, err, start)
	}
	if h := hitsOf(all...); !slices.Equal(h, []int32{1, 1, 1, 1}) {
		t.Fatalf("hits = %v", h)
	}
	if _, err := fetchAny(context.Background(), hc, ts, &start, p); err != nil {
		t.Fatal(err)
	}
	if h := hitsOf(all...); !slices.Equal(h, []int32{1, 1, 1, 2}) {
		t.Fatalf("sticky hits = %v", h)
	}

	bad, good2 := statusStub(t, http.StatusInternalServerError), okStub(t)
	start = 1
	if _, err := fetchAny(context.Background(), hc, tmpls(good2, bad), &start, p); err != nil {
		t.Fatal(err)
	}
	if start != 0 || bad.hits.Load() != 1 || good2.hits.Load() != 1 {
		t.Fatalf("wrap: start %d hits %v", start, hitsOf(good2, bad))
	}
}

func TestFetchAnyTimeoutMovesOn(t *testing.T) {
	slow := newHostStub(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	})
	good := okStub(t)
	start := 0
	got, err := fetchAny(context.Background(), &http.Client{Timeout: 200 * time.Millisecond}, tmpls(slow, good), &start, "Definitions/ok.yml")
	if err != nil || len(got.Links) != 3 || start != 1 {
		t.Fatalf("got %+v err %v start %d", got, err, start)
	}
}

func TestFetchAnyErrors(t *testing.T) {
	const p = "Definitions/ok.yml"
	hc := &http.Client{Timeout: 2 * time.Second}

	one := statusStub(t, http.StatusInternalServerError)
	start := 0
	if _, err := fetchAny(context.Background(), hc, tmpls(one), &start, p); err == nil || err.Error() != "status 500" {
		t.Fatalf("single: %v", err)
	}

	a, b := statusStub(t, http.StatusBadGateway), statusStub(t, http.StatusInternalServerError)
	start = 0
	_, err := fetchAny(context.Background(), hc, tmpls(a, b), &start, p)
	host := strings.TrimPrefix(b.srv.URL, "http://")
	if err == nil || !strings.HasSuffix(err.Error(), "status 500") || !strings.HasPrefix(err.Error(), host+": ") {
		t.Fatalf("all failed: %v (want last host %s)", err, host)
	}

	c, d := okStub(t), okStub(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start = 0
	if _, err := fetchAny(ctx, hc, tmpls(c, d), &start, p); err == nil {
		t.Fatal("canceled: expected error")
	}
	if d.hits.Load() != 0 {
		t.Fatalf("second host got %d requests after cancel", d.hits.Load())
	}
}

func TestClient(t *testing.T) {
	const proxy = "http://p.example:3128"
	hc := Client(config.MirrorSource{}, proxy, 30*time.Second)
	if hc.Timeout != 30*time.Second || hc.Transport != helper.ProxyTransport(proxy) || hc.Transport == nil {
		t.Fatalf("proxy client: %+v", hc)
	}
	if hc := Client(config.MirrorSource{Direct: true}, proxy, 7*time.Second); hc.Transport != nil || hc.Timeout != 7*time.Second {
		t.Fatalf("direct client: %+v", hc)
	}
	if hc := Client(config.MirrorSource{}, "", 30*time.Second); hc.Transport != nil {
		t.Fatalf("empty proxy: %+v", hc)
	}
}

func TestTestSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+jackettRoot+kinozalPath {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(linksYAML("a.example", "b.example", "c.example")))
	}))
	defer srv.Close()
	n, err := TestSource(context.Background(), srv.Client(), []string{srv.URL + "/{path}"})
	if err != nil || n != 3 {
		t.Fatalf("got %d, %v", n, err)
	}
	if _, err := TestSource(context.Background(), srv.Client(), []string{srv.URL + "/missing/{path}"}); err == nil {
		t.Fatal("404: expected error")
	}
}

func TestClientRefusesUnsafeRedirect(t *testing.T) {
	via := []*http.Request{{}}
	cases := []struct {
		target string
		via    []*http.Request
		refuse bool
	}{
		{"https://cdn.example.org/x", via, false},
		{"http://cdn.example.org/x", via, true},
		{"https://192.168.1.5/x", via, true},
		{"https://127.0.0.1:8080/x", via, true},
		{"https://Router.LOCAL./x", via, true},
		{"https://intranet/x", via, true},
		{"https://cdn.example.org/x", make([]*http.Request, 10), true},
	}
	for _, ms := range []config.MirrorSource{{Direct: true}, {}} {
		hc := Client(ms, "", time.Second)
		if hc.CheckRedirect == nil {
			t.Fatalf("direct=%v: CheckRedirect not set", ms.Direct)
		}
		for _, c := range cases {
			req, err := http.NewRequest(http.MethodGet, c.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := hc.CheckRedirect(req, c.via); (err != nil) != c.refuse {
				t.Errorf("direct=%v %s via=%d: err=%v, want refuse=%v", ms.Direct, c.target, len(c.via), err, c.refuse)
			}
		}
	}
}
