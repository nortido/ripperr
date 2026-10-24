package tlsclient

import (
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	fhttp "github.com/bogdanfinn/fhttp"
)

func TestNewHidesProxySecrets(t *testing.T) {
	for _, raw := range []string{"http://u:SEC-pw@/SEC-path", "http://u:SEC-pw@host:bad"} {
		_, err := New(Config{Profile: "chrome_131", ProxyURL: raw})
		if err == nil {
			t.Fatalf("%s: want error", raw)
		}
		if strings.Contains(err.Error(), "SEC-") {
			t.Fatalf("%s: secret leaked: %s", raw, err)
		}
	}
}

func TestDownloadRedirectToHTTPRefused(t *testing.T) {
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		t.Setenv(k, "")
	}
	var srv *httptest.Server
	srv = httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if r.URL.Path == "/a" {
			nethttp.Redirect(w, r, srv.URL+"/b", nethttp.StatusFound)
			return
		}
		w.WriteHeader(nethttp.StatusOK)
	}))
	defer srv.Close()

	c, err := New(Config{Profile: "chrome_131"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := nethttp.NewRequest(nethttp.MethodGet, srv.URL+"/a", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("want redirect error, got status %d", resp.StatusCode)
	}
	if !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("error without redirect: %v", err)
	}
	if !errors.Is(err, ErrRedirectRefused) {
		t.Fatalf("refusal lost through the client chain: %v", err)
	}
}

func TestDownloadRedirectPolicy(t *testing.T) {
	via := func(n int) []*fhttp.Request { return make([]*fhttp.Request, n) }
	cases := []struct {
		name string
		url  string
		via  int
		want string
	}{
		{"https public", "https://tracker.example.org/dl.php?t=1", 1, ""},
		{"http", "http://tracker.example.org/dl", 1, "redirect to http refused"},
		{"localhost", "https://localhost/dl", 1, "redirect to localhost refused"},
		{"loopback ip", "https://127.0.0.1/dl", 1, "redirect to 127.0.0.1 refused"},
		{"lan ip", "https://192.168.1.1/dl", 1, "redirect to 192.168.1.1 refused"},
		{"local name", "https://box.local/dl", 1, "redirect to box.local refused"},
		{"upper local name with dot", "https://BOX.LOCAL./dl", 1, "redirect to box.local refused"},
		{"10 hops", "https://tracker.example.org/dl", 10, "stopped after 10 redirects"},
		{"9 hops", "https://tracker.example.org/dl", 9, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.url)
			if err != nil {
				t.Fatal(err)
			}
			err = checkDownloadRedirect(&fhttp.Request{URL: u}, via(tc.via))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			// петля редиректов (мертвые куки гоняют на логин) остается подозрением на сессию
			if refused := errors.Is(err, ErrRedirectRefused); refused != (tc.via < 10) {
				t.Fatalf("errors.Is(%v, ErrRedirectRefused) = %v", err, refused)
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}
