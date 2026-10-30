package tlscapture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

const (
	testUA     = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36"
	testSecCH  = `"Chromium";v="146", "Not-A.Brand";v="24", "Google Chrome";v="146"`
	testAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
)

var testHeaderOrder = []string{
	"sec-ch-ua", "sec-ch-ua-mobile", "sec-ch-ua-platform", "upgrade-insecure-requests",
	"user-agent", "accept", "sec-fetch-site", "sec-fetch-mode", "sec-fetch-user",
	"sec-fetch-dest", "accept-encoding", "accept-language", "priority",
}

func testBrowser(t *testing.T) tls_client.HttpClient {
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
	return c
}

func browserGet(c tls_client.HttpClient, target string) (int, error) {
	req, err := fhttp.NewRequest(fhttp.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	req.Header = fhttp.Header{
		"sec-ch-ua":                 {testSecCH},
		"sec-ch-ua-mobile":          {"?0"},
		"sec-ch-ua-platform":        {`"Linux"`},
		"upgrade-insecure-requests": {"1"},
		"user-agent":                {testUA},
		"accept":                    {testAccept},
		"sec-fetch-site":            {"none"},
		"sec-fetch-mode":            {"navigate"},
		"sec-fetch-user":            {"?1"},
		"sec-fetch-dest":            {"document"},
		"accept-encoding":           {"gzip, deflate, br, zstd"},
		"accept-language":           {"en-US,en;q=0.9"},
		"priority":                  {"u=0, i"},
		fhttp.HeaderOrderKey:        testHeaderOrder,
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func testReceiver(t *testing.T) *Receiver {
	t.Helper()
	r, err := Listen(Options{ListenAddr: "127.0.0.1:0", AdvertiseHost: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

func captureBrowser(t *testing.T, r *Receiver, c tls_client.HttpClient) *Fingerprint {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fp, err := r.Capture(ctx, func(_ context.Context, target string) error {
		code, err := browserGet(c, target)
		if err == nil && code != 200 {
			err = fmt.Errorf("status %d", code)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func TestCaptureChromeProfile(t *testing.T) {
	r := testReceiver(t)
	fp := captureBrowser(t, r, testBrowser(t))

	wantOrder := profiles.Chrome_146.GetSettingsOrder()
	wantVals := profiles.Chrome_146.GetSettings()
	if len(fp.Settings) != len(wantOrder) {
		t.Fatalf("settings %v, want order %v", fp.Settings, wantOrder)
	}
	for i, id := range wantOrder {
		got := fp.Settings[i]
		if got.ID != uint16(id) || got.Val != wantVals[id] {
			t.Errorf("setting %d = %+v, want id %d val %d", i, got, id, wantVals[id])
		}
	}
	if want := profiles.Chrome_146.GetConnectionFlow(); fp.ConnectionFlow != want {
		t.Errorf("ConnectionFlow = %d, want %d", fp.ConnectionFlow, want)
	}
	if want := []string{":method", ":authority", ":scheme", ":path"}; !reflect.DeepEqual(fp.PseudoHeaderOrder, want) {
		t.Errorf("PseudoHeaderOrder = %v", fp.PseudoHeaderOrder)
	}
	if !reflect.DeepEqual(fp.HeaderOrder, testHeaderOrder) {
		t.Errorf("HeaderOrder = %v", fp.HeaderOrder)
	}
	if fp.Headers["user-agent"] != testUA || fp.Headers["sec-ch-ua"] != testSecCH {
		t.Errorf("Headers = %v", fp.Headers)
	}
	if len(fp.ClientHello) < 6 || fp.ClientHello[0] != 0x16 || fp.ClientHello[5] != 0x01 {
		t.Fatalf("ClientHello head % x", fp.ClientHello[:min(8, len(fp.ClientHello))])
	}
	ja4, err := JA4(fp.ClientHello)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ja4, "t13i") {
		t.Errorf("JA4 = %s", ja4)
	}
	t.Logf("Chrome_146 JA4 %s settings %+v flow %d", ja4, fp.Settings, fp.ConnectionFlow)
}

func TestCaptureTwiceOnOneReceiver(t *testing.T) {
	r := testReceiver(t)
	c := testBrowser(t)
	var urls []string
	var fps []*Fingerprint
	for range 2 {
		fp, err := r.Capture(context.Background(), func(_ context.Context, target string) error {
			urls = append(urls, target)
			_, err := browserGet(c, target)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		fps = append(fps, fp)
	}
	if urls[0] == urls[1] {
		t.Fatalf("same url twice: %s", urls[0])
	}
	if !reflect.DeepEqual(fps[0].Headers, fps[1].Headers) || !reflect.DeepEqual(fps[0].HeaderOrder, fps[1].HeaderOrder) {
		t.Errorf("second capture differs: %v vs %v", fps[0].Headers, fps[1].Headers)
	}
	if !fps[0].Matches(fps[1]) {
		t.Error("two captures of one browser do not match")
	}
}

func TestCaptureIgnoresForeignPath(t *testing.T) {
	r := testReceiver(t)
	c := testBrowser(t)
	foreign := 0
	fp, err := r.Capture(context.Background(), func(_ context.Context, target string) error {
		u, err := url.Parse(target)
		if err != nil {
			return err
		}
		u.Path = "/other"
		foreign, err = browserGet(c, u.String())
		if err != nil {
			return err
		}
		_, err = browserGet(c, target)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if foreign != 404 {
		t.Errorf("foreign status = %d, want 404", foreign)
	}
	if fp.Headers["user-agent"] != testUA || !reflect.DeepEqual(fp.HeaderOrder, testHeaderOrder) {
		t.Errorf("capture is not the token request: %v %v", fp.Headers, fp.HeaderOrder)
	}
}

func TestCaptureOpenError(t *testing.T) {
	r := testReceiver(t)
	_, err := r.Capture(context.Background(), func(context.Context, string) error { return errors.New("boom") })
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestCaptureNoRequest(t *testing.T) {
	r := testReceiver(t)
	if _, err := r.Capture(context.Background(), func(context.Context, string) error { return nil }); err == nil {
		t.Fatal("want error when nothing connected")
	}
}

func TestCloseRefusesConnections(t *testing.T) {
	r := testReceiver(t)
	var addr string
	_, err := r.Capture(context.Background(), func(_ context.Context, target string) error {
		u, err := url.Parse(target)
		if err != nil {
			return err
		}
		addr = u.Host
		return errors.New("stop")
	})
	if err == nil {
		t.Fatal("want error")
	}
	r.Close()
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		_ = conn.Close()
		t.Fatal("dial after Close succeeded")
	}
}

func TestCloseStopsGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	r, err := Listen(Options{ListenAddr: "127.0.0.1:0", AdvertiseHost: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	c := testBrowser(t)
	captureBrowser(t, r, c)
	r.Close()
	c.CloseIdleConnections()
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		buf := make([]byte, 1<<16)
		t.Fatalf("goroutines %d, before %d\n%s", got, before, buf[:runtime.Stack(buf, true)])
	}
}

func TestMatches(t *testing.T) {
	r := testReceiver(t)
	a := captureBrowser(t, r, testBrowser(t))
	b := captureBrowser(t, r, testBrowser(t))
	if !a.Matches(b) {
		t.Fatal("same profile captures must match")
	}

	changed := *b
	changed.Settings = append([]Setting(nil), b.Settings...)
	changed.Settings[0].Val++
	if a.Matches(&changed) {
		t.Error("changed setting value still matches")
	}

	flow := *b
	flow.ConnectionFlow++
	if a.Matches(&flow) {
		t.Error("changed connection flow still matches")
	}

	pseudo := *b
	pseudo.PseudoHeaderOrder = []string{":method", ":path", ":authority", ":scheme"}
	if a.Matches(&pseudo) {
		t.Error("changed pseudo order still matches")
	}

	other := *b
	other.ClientHello = vectorHello(0x0303, v2Ciphers, v2Exts, v2Sig, []string{"h2"}, nil)
	if a.Matches(&other) {
		t.Error("different JA4 still matches")
	}

	broken := *b
	broken.ClientHello = []byte{1, 2, 3}
	if broken.Matches(&broken) || a.Matches(nil) {
		t.Error("unparsable or nil fingerprint matches")
	}
}

func TestAdvertiseHost(t *testing.T) {
	got, err := AdvertiseHost("http://127.0.0.1:8191/v1")
	if err != nil || got != "127.0.0.1" {
		t.Fatalf("AdvertiseHost = %q, %v", got, err)
	}
	if _, err := AdvertiseHost("not a url"); err == nil {
		t.Fatal("want error for a string without host")
	}
}
