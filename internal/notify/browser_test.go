package notify

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"ripperr/internal/metrics"
	"ripperr/internal/system/logger"
	"ripperr/internal/webpush"
)

type memSettings struct {
	mu   sync.Mutex
	rows map[string]json.RawMessage
}

func (m *memSettings) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.rows[key]
	return bytes.Clone(v), ok, nil
}

func (m *memSettings) SaveSetting(_ context.Context, key string, value json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[key] = bytes.Clone(value)
	return nil
}

type pushTarget struct {
	mu     sync.Mutex
	script []int
	hits   int
	header http.Header
	body   []byte
	reply  string
}

func (p *pushTarget) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hits
}

type pushFixture struct {
	srv    *httptest.Server
	store  *webpush.Store
	pubKey string
	a, b   *pushTarget
}

func newPushFixture(t *testing.T, scriptA, scriptB []int) *pushFixture {
	t.Helper()
	f := &pushFixture{a: &pushTarget{script: scriptA}, b: &pushTarget{script: scriptB}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := map[string]*pushTarget{"/a": f.a, "/b": f.b}[r.URL.Path]
		if target == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		target.mu.Lock()
		target.hits++
		status := target.script[min(target.hits, len(target.script))-1]
		if target.hits == 1 {
			target.header = r.Header.Clone()
			target.body = body
		}
		reply := target.reply
		target.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(f.srv.Close)
	f.store = webpush.NewStore(&memSettings{rows: map[string]json.RawMessage{}})
	pub, err := f.store.PublicKey(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.pubKey = pub
	f.subscribe(t, f.srv.URL+"/a", "Dev A")
	f.subscribe(t, f.srv.URL+"/b", "Dev B")
	return f
}

func (f *pushFixture) subscribe(t *testing.T, endpoint, label string) {
	t.Helper()
	subscribeDevice(t, f.store, endpoint, label)
}

func subscribeDevice(t *testing.T, store *webpush.Store, endpoint, label string) {
	t.Helper()
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	var sub webpush.Subscription
	sub.Endpoint = endpoint
	sub.Keys.P256dh = base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes())
	sub.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
	if _, err := store.Subscribe(context.Background(), sub, label, "https://track.example", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func (f *pushFixture) notifier(cfg Config) *Notifier {
	cfg.Enabled = true
	n := New(cfg, "").WithPush(f.store)
	n.hc = f.srv.Client()
	return n
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitBrowser(t *testing.T, result string, want uint64) {
	t.Helper()
	waitUntil(t, "browser "+result+" counter", func() bool {
		return metrics.Notifications.Value("browser", result) >= want
	})
}

func TestNotifyBrowserDeliversToEveryDevice(t *testing.T) {
	defer withRetryDelay(time.Millisecond)()
	f := newPushFixture(t, []int{201}, []int{201})
	okBefore := metrics.Notifications.Value("browser", "ok")

	f.notifier(Config{}).Notify(UpdatedEvent("Some.Release", "https://example.com/t1", ""))

	waitBrowser(t, "ok", okBefore+1)
	for name, p := range map[string]*pushTarget{"a": f.a, "b": f.b} {
		if p.count() != 1 {
			t.Fatalf("%s hits = %d, want 1", name, p.count())
		}
		h := p.header
		if h.Get("Content-Encoding") != "aes128gcm" || h.Get("TTL") != "86400" {
			t.Errorf("%s headers: %v", name, h)
		}
		if auth := h.Get("Authorization"); !strings.HasPrefix(auth, "vapid t=") || !strings.Contains(auth, "k="+f.pubKey) {
			t.Errorf("%s Authorization = %q", name, auth)
		}
		if len(p.body) <= 102 {
			t.Errorf("%s body = %d bytes, want > 102", name, len(p.body))
		}
	}
}

func TestNotifyBrowserGoneRemovesDevice(t *testing.T) {
	defer withRetryDelay(time.Millisecond)()
	f := newPushFixture(t, []int{410}, []int{201})
	errBefore := metrics.Notifications.Value("browser", "error")
	logsBefore := countLogs("notify: browser", "Dev A")

	f.notifier(Config{}).Notify(UpdatedEvent("Some.Release", "https://example.com/t1", ""))

	waitBrowser(t, "error", errBefore+1)
	if f.a.count() != 1 || f.b.count() != 1 {
		t.Fatalf("hits a=%d b=%d, want 1 and 1", f.a.count(), f.b.count())
	}
	_, devs, err := f.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].Label != "Dev B" {
		t.Fatalf("devices after 410 = %+v, want only Dev B", devs)
	}
	if got := countLogs("notify: browser", "Dev A") - logsBefore; got != 1 {
		t.Fatalf("Warn entries naming Dev A = %d, want 1", got)
	}
	for _, e := range logger.Snapshot(200) {
		if strings.Contains(e.Message, f.srv.URL+"/a") {
			t.Fatalf("endpoint leaked into log: %s", e.Message)
		}
	}
}

func countLogs(parts ...string) int {
	count := 0
	for _, e := range logger.Snapshot(500) {
		match := true
		for _, p := range parts {
			match = match && strings.Contains(e.Message, p)
		}
		if match {
			count++
		}
	}
	return count
}

func TestNotifyBrowserRetriesOnlyFailed(t *testing.T) {
	defer withRetryDelay(time.Millisecond)()

	f := newPushFixture(t, []int{500, 201}, []int{201})
	okBefore := metrics.Notifications.Value("browser", "ok")
	f.notifier(Config{}).Notify(UpdatedEvent("Some.Release", "https://example.com/t1", ""))
	waitBrowser(t, "ok", okBefore+1)
	if f.a.count() != 2 || f.b.count() != 1 {
		t.Fatalf("hits a=%d b=%d, want 2 and 1", f.a.count(), f.b.count())
	}

	f = newPushFixture(t, []int{410}, []int{500, 201})
	okBefore = metrics.Notifications.Value("browser", "ok")
	f.notifier(Config{}).Notify(UpdatedEvent("Some.Release", "https://example.com/t1", ""))
	waitBrowser(t, "ok", okBefore+1)
	if f.a.count() != 1 || f.b.count() != 2 {
		t.Fatalf("second case hits a=%d b=%d, want 1 and 2", f.a.count(), f.b.count())
	}
	_, devs, err := f.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].Label != "Dev B" {
		t.Fatalf("devices = %+v, want only Dev B", devs)
	}
}

func TestNotifyBrowserFlags(t *testing.T) {
	defer withRetryDelay(time.Millisecond)()
	quiet := func() { time.Sleep(150 * time.Millisecond) }

	f := newPushFixture(t, []int{201}, []int{201})
	n := f.notifier(Config{BrowserDisabled: true})
	n.Notify(UpdatedEvent("Some.Release", "https://example.com/t1", ""))
	quiet()
	if f.a.count()+f.b.count() != 0 {
		t.Fatalf("BrowserDisabled still delivered: a=%d b=%d", f.a.count(), f.b.count())
	}
	if err := n.Send(context.Background(), TestEvent("")); err != nil {
		t.Fatalf("Send with BrowserDisabled: %v", err)
	}
	if f.a.count() != 1 || f.b.count() != 1 {
		t.Fatalf("Send hits a=%d b=%d, want 1 and 1", f.a.count(), f.b.count())
	}

	f = newPushFixture(t, []int{201}, []int{201})
	f.notifier(Config{BrowserEvents: []string{KindBroken}}).Notify(UpdatedEvent("Some.Release", "https://example.com/t1", ""))
	quiet()
	if f.a.count()+f.b.count() != 0 {
		t.Fatalf("BrowserEvents [broken] delivered updated: a=%d b=%d", f.a.count(), f.b.count())
	}

	empty := webpush.NewStore(&memSettings{rows: map[string]json.RawMessage{}})
	for name, n := range map[string]*Notifier{
		"no devices": New(Config{Enabled: true}, "").WithPush(empty),
		"nil store":  New(Config{Enabled: true}, "").WithPush(nil),
	} {
		if n.HasTransport() {
			t.Errorf("%s: HasTransport = true", name)
		}
		err := n.Send(context.Background(), TestEvent(""))
		if err == nil || err.Error() != "no notification transport configured" {
			t.Errorf("%s: Send error = %v", name, err)
		}
	}

	hook := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer hook.Close()
	if err := New(Config{Enabled: true, WebhookURL: hook.URL}, "").WithPush(nil).Send(context.Background(), TestEvent("")); err != nil {
		t.Errorf("WithPush(nil) with webhook: %v", err)
	}
}

func TestSendBrowserErrorsHideEndpoint(t *testing.T) {
	const token = "Zq3Xw8LmN4pR7sT2vB5cY9dK"
	const closedPath = "/closed-path-secret-abcdef"

	tokenSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("denied for " + r.URL.Path))
	}))
	defer tokenSrv.Close()
	closed := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()

	store := webpush.NewStore(&memSettings{rows: map[string]json.RawMessage{}})
	if _, err := store.PublicKey(context.Background()); err != nil {
		t.Fatal(err)
	}
	subscribeDevice(t, store, tokenSrv.URL+"/"+token, "Dev A")
	subscribeDevice(t, store, closedURL+closedPath, "Dev C")
	n := New(Config{Enabled: true}, "").WithPush(store)
	n.hc = tokenSrv.Client()

	err := n.Send(context.Background(), TestEvent(""))
	if err == nil {
		t.Fatal("want error")
	}
	msg := err.Error()
	for _, want := range []string{"browser: Dev A", "status 403"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}
	for _, secret := range []string{token, closedPath} {
		if strings.Contains(msg, secret) {
			t.Errorf("error leaks %q: %s", secret, msg)
		}
	}
}

func decodePayload(t *testing.T, raw []byte) (title, body, url string) {
	t.Helper()
	var p struct {
		Title string `json:"title"`
		Body  string `json:"body"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("payload is not JSON: %v: %q", err, raw)
	}
	return p.Title, p.Body, p.URL
}

func TestBrowserPayload(t *testing.T) {
	n := New(Config{}, "")
	ev := n.localize(UpdatedEvent("Some.Release", "https://example.com/t1", ""))
	title, body, url := decodePayload(t, browserPayload(ev))
	if url != "/"+ev.AppPath || title != subjectLine(ev) || body != ev.plain() {
		t.Errorf("payload = %q %q %q, want %q %q %q", title, body, url, subjectLine(ev), ev.plain(), "/"+ev.AppPath)
	}

	if _, _, url := decodePayload(t, browserPayload(n.localize(TestEvent("")))); url != "/" {
		t.Errorf("test event url = %q, want /", url)
	}

	ru := New(Config{Language: "ru"}, "").localize(TrackerUpEvent("rutracker"))
	ruTitle, _, _ := decodePayload(t, browserPayload(ru))
	if !strings.ContainsFunc(ruTitle, func(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }) {
		t.Errorf("ru title %q has no Cyrillic", ruTitle)
	}

	long := Event{Kind: KindUpdated, Text: strings.Repeat("Ж"+strings.Repeat("\U0001F600", 9), 300)}
	raw := browserPayload(long)
	if len(raw) > webpush.MaxPayload {
		t.Errorf("payload = %d bytes, want <= %d", len(raw), webpush.MaxPayload)
	}
	_, lbody, _ := decodePayload(t, raw)
	if lbody == "" || !strings.HasPrefix(long.Text, lbody) || len([]rune(lbody)) >= 1000 {
		t.Errorf("long body = %d runes, want a non-empty prefix cut below 1000", len([]rune(lbody)))
	}

	if raw := browserPayload(Event{Kind: KindUpdated, Text: "a<b&c"}); !bytes.Contains(raw, []byte("a<b&c")) {
		t.Errorf("payload escaped HTML: %q", raw)
	}
}

func TestBrowserUnreadableRowWarnsOnce(t *testing.T) {
	rows := &memSettings{rows: map[string]json.RawMessage{}}
	n := New(Config{Enabled: true}, "").WithPush(webpush.NewStore(rows))
	broken := json.RawMessage(`{"devices":1}`)
	before := countLogs("notify: browser")

	n.HasTransport()
	rows.mu.Lock()
	rows.rows["webpush"] = broken
	rows.mu.Unlock()
	for range 3 {
		n.HasTransport()
	}
	if got := countLogs("notify: browser") - before; got != 1 {
		t.Fatalf("Warn entries for 3 failed reads = %d, want 1", got)
	}

	rows.mu.Lock()
	delete(rows.rows, "webpush")
	rows.mu.Unlock()
	n.HasTransport()
	rows.mu.Lock()
	rows.rows["webpush"] = broken
	rows.mu.Unlock()
	n.HasTransport()
	if got := countLogs("notify: browser") - before; got != 2 {
		t.Fatalf("Warn entries after recovery and new failure = %d, want 2", got)
	}
}
