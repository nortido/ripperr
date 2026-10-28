package settings

import (
	"strings"
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

func TestNormalizeIntegrationsPatchTransmissionHost(t *testing.T) {
	host := "localhost:9091"
	o := integrationsOverlay{}
	o.Transmission.Host = &host
	if err := normalizeIntegrationsPatch(&o); err != nil {
		t.Fatal(err)
	}
	if *o.Transmission.Host != "http://localhost:9091" {
		t.Errorf("transmission host = %q", *o.Transmission.Host)
	}
}

func TestNormalizeIntegrationsPatchDelugeHost(t *testing.T) {
	host := "localhost:8112"
	o := integrationsOverlay{}
	o.Deluge.Host = &host
	if err := normalizeIntegrationsPatch(&o); err != nil {
		t.Fatal(err)
	}
	if *o.Deluge.Host != "http://localhost:8112" {
		t.Errorf("deluge host = %q", *o.Deluge.Host)
	}
}

func TestURLValidationErrorsHideRawURL(t *testing.T) {
	_, ensureErr := EnsureScheme("http://u:SEC-pw@host:bad/SEC-path", "http")
	for name, err := range map[string]error{
		"EnsureScheme":          ensureErr,
		"ValidateDownloadProxy": ValidateDownloadProxy("socks5://u:SEC-pw@host:bad"),
		"ValidateWebhookURL":    ValidateWebhookURL("https://u:SEC-pw@host:bad/SEC-path?q=SEC-q"),
	} {
		if err == nil {
			t.Errorf("%s: expected error", name)
			continue
		}
		if msg := err.Error(); strings.Contains(msg, "SEC-") || !strings.Contains(msg, "invalid port") {
			t.Errorf("%s: %s", name, msg)
		}
	}
}

func TestURLParseErrorsQuoteNoInput(t *testing.T) {
	for _, raw := range []string{"socks5://u:abc/def@p:1080", "http://abc def.example", "http://h/%zzabc"} {
		err := ValidateDownloadProxy(raw)
		if raw != "socks5://u:abc/def@p:1080" {
			_, err = EnsureScheme(raw, "http")
		}
		if err == nil {
			t.Errorf("%s: expected error", raw)
			continue
		}
		if msg := err.Error(); strings.Contains(msg, "abc") || !strings.HasPrefix(msg, "invalid url") {
			t.Errorf("%s: %s", raw, msg)
		}
	}
}

func TestNormalizeIntegrationsPatchDownloadStationHost(t *testing.T) {
	host := "nas:5000"
	o := integrationsOverlay{}
	o.DownloadStation.Host = &host
	if err := normalizeIntegrationsPatch(&o); err != nil {
		t.Fatal(err)
	}
	if *o.DownloadStation.Host != "http://nas:5000" {
		t.Errorf("downloadstation host = %q", *o.DownloadStation.Host)
	}
}
