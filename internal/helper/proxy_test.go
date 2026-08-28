package helper

import (
	"net/http"
	"testing"
)

func TestProxyTransportEmptyIsNilInterface(t *testing.T) {
	// именно интерфейсный nil: типизированный сломал бы откат http.Client на DefaultTransport
	if rt := ProxyTransport("  "); rt != nil {
		t.Fatalf("want nil RoundTripper, got %T", rt)
	}
	if rt := ProxyTransport("://broken"); rt != nil {
		t.Fatalf("want nil RoundTripper for broken url, got %T", rt)
	}
}

func TestProxyTransportRoutesThroughProxy(t *testing.T) {
	rt := ProxyTransport("socks5://127.0.0.1:1080")
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("want *http.Transport, got %T", rt)
	}
	req, err := http.NewRequest(http.MethodGet, "https://rutracker.org/forum/index.php", nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.String() != "socks5://127.0.0.1:1080" {
		t.Fatalf("want proxy socks5://127.0.0.1:1080, got %v", u)
	}
}

func TestProxyTransportHonoursNoProxy(t *testing.T) {
	t.Setenv("NO_PROXY", "homeassistant.lan")
	tr := ProxyTransport("http://127.0.0.1:3128").(*http.Transport)
	for _, tc := range []struct {
		target  string
		proxied bool
	}{
		{"http://homeassistant.lan/api/webhook/x", false},
		{"http://localhost:8123/api/webhook/x", false},
		{"https://api.telegram.org/bot1/sendMessage", true},
	} {
		req, err := http.NewRequest(http.MethodGet, tc.target, nil)
		if err != nil {
			t.Fatal(err)
		}
		u, err := tr.Proxy(req)
		if err != nil {
			t.Fatal(err)
		}
		if (u != nil) != tc.proxied {
			t.Fatalf("%s: want proxied=%v, got %v", tc.target, tc.proxied, u)
		}
	}
}

func TestDirectTransportNeverProxies(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:3128")
	if tr := DirectTransport(); tr.Proxy != nil {
		t.Fatal("want nil Proxy on direct transport")
	}
}
