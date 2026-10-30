package tlsclient

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"

	"ripperr/internal/helper/tlscapture"
)

func newTestReceiver(t *testing.T, host string) *tlscapture.Receiver {
	t.Helper()
	r, err := tlscapture.Listen(tlscapture.Options{ListenAddr: "127.0.0.1:0", AdvertiseHost: host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

func captureOpen(t *testing.T, r *tlscapture.Receiver, open func(ctx context.Context, target string) error) *tlscapture.Fingerprint {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fp, err := r.Capture(ctx, open)
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func captureBrowser(t *testing.T, r *tlscapture.Receiver) *tlscapture.Fingerprint {
	t.Helper()
	c, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithClientProfile(profiles.Chrome_146),
		tls_client.WithInsecureSkipVerify(),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithTimeoutSeconds(10),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.CloseIdleConnections)
	return captureOpen(t, r, func(_ context.Context, target string) error {
		req, err := fhttp.NewRequest(fhttp.MethodGet, target, nil)
		if err != nil {
			return err
		}
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != 200 {
			return fmt.Errorf("status %d", resp.StatusCode)
		}
		return nil
	})
}

func captureProbe(t *testing.T, r *tlscapture.Receiver, cfg Config) *tlscapture.Fingerprint {
	t.Helper()
	cfg.Direct = true
	cfg.NoRedirects = true
	return captureOpen(t, r, func(ctx context.Context, target string) error {
		return Probe(ctx, cfg, target)
	})
}

func ja4Of(t *testing.T, fp *tlscapture.Fingerprint) string {
	t.Helper()
	s, err := tlscapture.JA4(fp.ClientHello)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestProbeBuiltinMatchesBrowser(t *testing.T) {
	r := newTestReceiver(t, "127.0.0.1")
	a := captureBrowser(t, r)
	b := captureProbe(t, r, Config{Profile: "chrome_146"})
	if !a.Matches(b) {
		t.Fatalf("browser JA4 %s, probe JA4 %s", ja4Of(t, a), ja4Of(t, b))
	}
}

func TestFingerprintProfileReproducesCapture(t *testing.T) {
	r := newTestReceiver(t, "127.0.0.1")
	a := captureBrowser(t, r)
	c := captureProbe(t, r, Config{Profile: "chrome_103", Fingerprint: a})
	if !a.Matches(c) {
		t.Fatalf("captured JA4 %s, custom JA4 %s", ja4Of(t, a), ja4Of(t, c))
	}
	d := captureProbe(t, r, Config{Profile: "chrome_103"})
	if a.Matches(d) {
		t.Fatalf("base chrome_103 already matches the capture: %s", ja4Of(t, d))
	}
	t.Logf("capture JA4 %s, base chrome_103 JA4 %s", ja4Of(t, a), ja4Of(t, d))
}

func TestFingerprintProfileAddsSNI(t *testing.T) {
	a := captureBrowser(t, newTestReceiver(t, "127.0.0.1"))
	if got := ja4Of(t, a); got[3] != 'i' {
		t.Fatalf("capture over IP has SNI: %s", got)
	}
	r := newTestReceiver(t, "localhost")
	c := captureProbe(t, r, Config{Profile: "chrome_103", Fingerprint: a})
	if got := ja4Of(t, c); got[3] != 'd' {
		t.Fatalf("no SNI in custom hello over a host name: %s", got)
	}
}

func TestFingerprintSpecFresh(t *testing.T) {
	a := captureBrowser(t, newTestReceiver(t, "127.0.0.1"))
	p, err := capturedProfile(profiles.Chrome_103, a)
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.GetClientHelloSpec()
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.GetClientHelloSpec()
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []tls.ClientHelloSpec{first, second} {
		var sni bool
		for _, ext := range spec.Extensions {
			if _, ok := ext.(*tls.SNIExtension); ok {
				sni = true
			}
		}
		if !sni {
			t.Fatal("spec has no SNI extension")
		}
	}
	for i, ext := range first.Extensions {
		for j, other := range second.Extensions {
			// у пустых структур все указатели равны, это не общее состояние
			if ext == other && reflect.TypeOf(ext).Elem().Size() > 0 {
				t.Fatalf("extension %d of the first spec is shared with %d of the second: %T", i, j, ext)
			}
		}
	}
}

func TestNewRejectsBadHello(t *testing.T) {
	_, err := New(Config{Profile: "chrome_146", Fingerprint: &tlscapture.Fingerprint{ClientHello: []byte{1, 2, 3}}})
	if err == nil {
		t.Fatal("want error for a bad ClientHello")
	}
}

func TestChromeMajor(t *testing.T) {
	cases := []struct {
		ua    string
		major int
		ok    bool
	}{
		{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36", 152, true},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0", 0, false},
	}
	for _, tc := range cases {
		got, ok := ChromeMajor(tc.ua)
		if got != tc.major || ok != tc.ok {
			t.Errorf("ChromeMajor(%q) = %d, %v; want %d, %v", tc.ua, got, ok, tc.major, tc.ok)
		}
	}
}
