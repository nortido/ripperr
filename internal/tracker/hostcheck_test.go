package tracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	proxyModel "ripperr/internal/model/proxy"
)

func checkHost(t *testing.T, status int, location, body string) (bool, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if location != "" {
			w.Header().Set("Location", location)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{
		Transport:     &redirectTransport{host: target.Host},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return HostChecker(hc)(context.Background(), "mirror.example", int(proxyModel.TypeNone))
}

func TestHostCheckerLocation(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		location string
		body     string
		alive    bool
		host     string
	}{
		{"absolute redirect", http.StatusFound, "https://new.example/index.php", "", true, "new.example"},
		{"relative redirect", http.StatusFound, "/login.php", "", true, "mirror.example"},
		{"ok page", http.StatusOK, "", "<title>tracker</title>", true, ""},
		{"server error", http.StatusInternalServerError, "", "<title>error</title>", false, ""},
		{"blank page", http.StatusOK, "", "<html> </html>", false, ""},
	}
	for _, c := range cases {
		alive, host := checkHost(t, c.status, c.location, c.body)
		if alive != c.alive || host != c.host {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", c.name, alive, host, c.alive, c.host)
		}
	}
}

func TestHostCheckerFlareDirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<title>mirror</title>"))
	}))
	defer srv.Close()
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	rt := &redirectTransport{host: target.Host}
	check := HostChecker(&http.Client{Transport: rt})

	if alive, _ := check(context.Background(), "mirror.example", int(proxyModel.TypeNone)); !alive {
		t.Fatal("want host alive")
	}
	if rt.hits != 1 {
		t.Fatalf("обычный трекер должен проверяться клиентом трекера, hits=%d", rt.hits)
	}
	_, _ = check(context.Background(), "mirror.invalid", int(proxyModel.TypeFlare))
	if rt.hits != 1 {
		t.Fatalf("проверка flare-трекера ушла транспортом трекера, hits=%d", rt.hits)
	}
}
