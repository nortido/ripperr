package settings

import (
	"testing"
)

func TestEnsureScheme(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{"with http", "http://host:8080", "http://host:8080", false},
		{"without scheme", "192.168.1.200:8191/v1", "http://192.168.1.200:8191/v1", false},
		{"localhost path", "localhost:8191/v1", "http://localhost:8191/v1", false},
		{"empty", "", "", true},
		{"no host", "http://", "", true},
		{"garbage scheme", "://x", "", true},
		{"already normalized", "http://http://host:8080", "http://http://host:8080", false},
		{"socks5 untouched", "socks5://127.0.0.1:1080", "socks5://127.0.0.1:1080", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := EnsureScheme(c.raw, "http")
			if c.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestValidateDownloadProxy(t *testing.T) {
	if err := ValidateDownloadProxy(""); err != nil {
		t.Fatal(err)
	}
	for _, ok := range []string{"http://proxy:8080", "https://proxy:8080", "socks5://127.0.0.1:1080"} {
		if err := ValidateDownloadProxy(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"ftp://proxy", "not-a-url", "socks5://"} {
		if err := ValidateDownloadProxy(bad); err == nil {
			t.Errorf("%q: expected error", bad)
		}
	}
}

func TestNormalizeIntegrationsPatch(t *testing.T) {
	host := "localhost:8068"
	url := "192.168.1.1:8191/v1"
	proxy := "socks5://127.0.0.1:1080"
	o := integrationsOverlay{}
	o.Qbit.Host = &host
	o.Flaresolverr.URL = &url
	o.Proxy.DownloadProxy = &proxy
	if err := normalizeIntegrationsPatch(&o); err != nil {
		t.Fatal(err)
	}
	if *o.Qbit.Host != "http://localhost:8068" {
		t.Errorf("qbit host = %q", *o.Qbit.Host)
	}
	if *o.Flaresolverr.URL != "http://192.168.1.1:8191/v1" {
		t.Errorf("flare url = %q", *o.Flaresolverr.URL)
	}
}
