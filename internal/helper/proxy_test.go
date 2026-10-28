package helper

import (
	"net/http"
	"strings"
	"testing"
)

func proxyFor(t *testing.T, target string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestProxyTransportEmptyIsNilInterface(t *testing.T) {
	// интерфейсный nil: с типизированным http.Client не откатится на DefaultTransport
	if rt := ProxyTransport("  "); rt != nil {
		t.Fatalf("want nil RoundTripper, got %T", rt)
	}
}

func TestProxyTransportBrokenAddressFailsClosed(t *testing.T) {
	// адрес без схемы частая опечатка, прямое соединение недопустимо: запросы с логинами
	// трекеров ушли бы с реального IP
	for _, raw := range []string{"1.2.3.4:8080", "://broken"} {
		tr, ok := ProxyTransport(raw).(*http.Transport)
		if !ok {
			t.Fatalf("%s: want *http.Transport", raw)
		}
		if _, err := tr.Proxy(proxyFor(t, "https://rutracker.org/forum/index.php")); err == nil {
			t.Fatalf("%s: want error, got direct connection", raw)
		}
	}
}

func TestProxyTransportRoutesThroughProxy(t *testing.T) {
	rt := ProxyTransport("socks5://127.0.0.1:1080")
	tr, ok := rt.(*http.Transport)
	if !ok {
		t.Fatalf("want *http.Transport, got %T", rt)
	}
	u, err := tr.Proxy(proxyFor(t, "https://rutracker.org/forum/index.php"))
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.String() != "socks5://127.0.0.1:1080" {
		t.Fatalf("want proxy socks5://127.0.0.1:1080, got %v", u)
	}
}

func TestProxyTransportKeepsLocalTargetsDirect(t *testing.T) {
	tr := ProxyTransport("http://127.0.0.1:3128").(*http.Transport)
	for _, tc := range []struct {
		target  string
		proxied bool
	}{
		// api.telegram.org в РФ недоступен напрямую, это и есть смысл прокси
		{"https://api.telegram.org/bot1/sendMessage", true},
		{"https://rutracker.org/forum/index.php", true},
		{"http://192.168.1.50:8123/api/webhook/x", false},
		{"http://10.0.0.5/api/webhook/x", false},
		{"http://172.16.3.4/api/webhook/x", false},
		{"http://[fd00::1]/api/webhook/x", false},
		{"http://nas.local:8123/api/webhook/x", false},
		{"http://qbittorrent:8080/api/v2/auth/login", false},
		{"http://localhost:8123/api/webhook/x", false},
	} {
		u, err := tr.Proxy(proxyFor(t, tc.target))
		if err != nil {
			t.Fatal(err)
		}
		if (u != nil) != tc.proxied {
			t.Fatalf("%s: want proxied=%v, got %v", tc.target, tc.proxied, u)
		}
	}
}

func TestProxyTransportHonoursNoProxy(t *testing.T) {
	t.Setenv("NO_PROXY", "mirror.rutracker.org")
	tr := ProxyTransport("http://127.0.0.1:3128").(*http.Transport)
	u, err := tr.Proxy(proxyFor(t, "https://mirror.rutracker.org/forum/index.php"))
	if err != nil {
		t.Fatal(err)
	}
	if u != nil {
		t.Fatalf("want direct for NO_PROXY host, got %v", u)
	}
}

func TestProxyTransportPrefersUpperCaseNoProxy(t *testing.T) {
	// склейка обеих переменных давала бы no_proxy=* поверх выбора пользователя,
	// и весь трафик тихо ушел бы мимо прокси
	t.Setenv("NO_PROXY", "mirror.rutracker.org")
	t.Setenv("no_proxy", "*")
	tr := ProxyTransport("http://127.0.0.1:3128").(*http.Transport)
	u, err := tr.Proxy(proxyFor(t, "https://rutracker.org/forum/index.php"))
	if err != nil {
		t.Fatal(err)
	}
	if u == nil {
		t.Fatal("want proxy, got direct: lower-case no_proxy won")
	}
}

func TestProxyTransportReusesTransport(t *testing.T) {
	if a, b := ProxyTransport("http://127.0.0.1:9999"), ProxyTransport("http://127.0.0.1:9999"); a != b {
		t.Fatal("want the same cached transport, got a fresh pool per call")
	}
}

func TestDirectTransportNeverProxies(t *testing.T) {
	if tr := DirectTransport(); tr.Proxy != nil {
		t.Fatal("want nil Proxy on direct transport")
	}
	// клон, а не сам DefaultTransport: обнуление Proxy на общем транспорте выключило
	// бы прокси всему процессу
	if http.DefaultTransport.(*http.Transport).Proxy == nil {
		t.Fatal("DirectTransport mutated http.DefaultTransport")
	}
}

func TestProxyTransportInvalidURLHidesSecrets(t *testing.T) {
	hc := &http.Client{Transport: ProxyTransport("socks5://u:SEC-pw@/SEC-path")}
	_, err := hc.Get("http://example.invalid")
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "SEC-") {
		t.Fatalf("secret leaked: %s", err)
	}
}
