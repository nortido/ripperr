package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ripperr/internal/metrics"
	"ripperr/internal/system/logger"
)

func TestEventTextsDistinguishTypesAndCarrySubject(t *testing.T) {
	cases := []struct {
		name    string
		ev      Event
		wantKw  string
		wantSub string
	}{
		{"updated", UpdatedEvent("Some.Release.2026", "https://example.com/t1", ""), "Release updated in the torrent client:", "Some.Release.2026"},
		{"broken", BrokenEvent("Broken.Release", "https://example.com/t2", 5), "ignored", "Broken.Release"},
		{"tracker down", TrackerDownEvent("rutracker", "connection refused"), "unavailable", "rutracker"},
		{"tracker up", TrackerUpEvent("rutracker"), "resumed", "rutracker"},
		{"mirror digest", MirrorDigestEvent([]MirrorDigestItem{{Tracker: "kinozal", Kind: DigestMain, Host: "kinozal.me", From: "kinozal.tv"}, {Tracker: "rutor", Kind: DigestRemove, Host: "rutor.example", Reason: DigestReasonDead}}), "Tracker mirrors", "rutor.example"},
		{"mirror paused", MirrorPausedEvent("kinozal", "kinozal.xyz"), "is paused", "kinozal.xyz"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(c.ev.plain(), c.wantKw) {
				t.Errorf("text %q does not contain distinguishing keyword %q", c.ev.plain(), c.wantKw)
			}
			if !strings.Contains(c.ev.plain(), c.wantSub) {
				t.Errorf("text %q does not contain subject %q", c.ev.plain(), c.wantSub)
			}
			for _, other := range cases {
				if other.name == c.name {
					continue
				}
				if strings.Contains(other.ev.plain(), c.wantKw) {
					t.Errorf("keyword %q meant to distinguish %q also appears in %q text", c.wantKw, c.name, other.name)
				}
			}
		})
	}
	if BrokenEvent("x", "", 5).plain() == "" || !strings.Contains(BrokenEvent("x", "", 3).plain(), "3") {
		t.Error("broken event text does not carry the streak count")
	}
}

func TestTrackerDownEventCarriesErrorText(t *testing.T) {
	ev := TrackerDownEvent("rutracker", "connection refused")
	if !strings.Contains(ev.plain(), "connection refused") {
		t.Errorf("text %q does not contain the error message", ev.plain())
	}
}

func TestHasTransport(t *testing.T) {
	if New(Config{}, "").HasTransport() {
		t.Error("empty config should have no transport")
	}
	if New(Config{TelegramToken: "tok"}, "").HasTransport() {
		t.Error("token without chat id should have no transport")
	}
	if !New(Config{TelegramToken: "tok", TelegramChatID: "1"}, "").HasTransport() {
		t.Error("token+chat id should have a transport")
	}
	if !New(Config{WebhookURL: "http://example.com"}, "").HasTransport() {
		t.Error("webhook url should have a transport")
	}
}

func TestSendWebhookBody(t *testing.T) {
	var got struct {
		Event string `json:"event"`
		Text  string `json:"text"`
		URL   string `json:"url"`
	}
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(Config{WebhookURL: srv.URL, Enabled: true}, "")
	ev := Event{Kind: KindUpdated, Text: "some release", URL: "https://example.com/x"}
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPost {
		t.Errorf("method = %s, want POST", method)
	}
	if got.Event != ev.Kind || got.Text != ev.Text || got.URL != ev.URL {
		t.Errorf("got %+v, want event/text/url from %+v", got, ev)
	}
}

func TestSendTelegramWhenTokenAndChatIDSet(t *testing.T) {
	orig := telegramAPIURL
	defer func() { telegramAPIURL = orig }()

	called := false
	var gotChatID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		var body struct {
			ChatID string `json:"chat_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotChatID = body.ChatID
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	telegramAPIURL = srv.URL + "/bot%s/sendMessage"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true}, "")
	if err := n.Send(context.Background(), Event{Kind: KindUpdated, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("telegram endpoint was not called")
	}
	if gotChatID != "42" {
		t.Errorf("chat_id = %q, want 42", gotChatID)
	}
}

func TestNotifySkipsWhenDisabled(t *testing.T) {
	hit := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(Config{WebhookURL: srv.URL, Enabled: false}, "")
	n.Notify(Event{Kind: KindUpdated, Text: "should not send"})

	// wants() проверяется синхронно еще до горутины доставки, так что неблокирующего
	// чтения хватает, ждать не нужно
	select {
	case <-hit:
		t.Fatal("webhook was called while notifications are disabled")
	default:
	}
}

func TestNotifyDeliversAsync(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(done)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := New(Config{WebhookURL: srv.URL, Enabled: true}, "")
	n.Notify(Event{Kind: KindUpdated, Text: "hi"})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook was not called")
	}
}

func TestResolveChatIDPicksLastMessage(t *testing.T) {
	orig := telegramGetUpdatesURL
	defer func() { telegramGetUpdatesURL = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":[
			{"update_id":1,"message":{"chat":{"id":111}}},
			{"update_id":2,"my_chat_member":{"chat":{"id":999}}},
			{"update_id":3,"message":{"chat":{"id":222}}}
		]}`))
	}))
	defer srv.Close()
	telegramGetUpdatesURL = srv.URL + "/bot%s/getUpdates"

	got, err := ResolveChatID(context.Background(), "tok", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "222" {
		t.Errorf("chat id = %q, want 222 (last message update, non-message updates skipped)", got)
	}
}

func TestResolveChatIDEmptyResultHintsAtStart(t *testing.T) {
	orig := telegramGetUpdatesURL
	defer func() { telegramGetUpdatesURL = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
	}))
	defer srv.Close()
	telegramGetUpdatesURL = srv.URL + "/bot%s/getUpdates"

	_, err := ResolveChatID(context.Background(), "tok", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "/start") {
		t.Errorf("error %q does not hint at /start", err.Error())
	}
}

func TestResolveChatIDOKFalseReturnsDescription(t *testing.T) {
	orig := telegramGetUpdatesURL
	defer func() { telegramGetUpdatesURL = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Unauthorized"}`))
	}))
	defer srv.Close()
	telegramGetUpdatesURL = srv.URL + "/bot%s/getUpdates"

	_, err := ResolveChatID(context.Background(), "tok", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Unauthorized") {
		t.Errorf("error %q does not contain telegram description", err.Error())
	}
}

func TestResolveChatIDConflictStatusVisibleInError(t *testing.T) {
	orig := telegramGetUpdatesURL
	defer func() { telegramGetUpdatesURL = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"ok":false,"description":"Conflict: can't use getUpdates method while webhook is active"}`))
	}))
	defer srv.Close()
	telegramGetUpdatesURL = srv.URL + "/bot%s/getUpdates"

	_, err := ResolveChatID(context.Background(), "tok", "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "409") {
		t.Errorf("error %q does not surface the 409 conflict status", err.Error())
	}
}

func TestSendErrorOn4xxIncludesBodySnippet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad payload: missing field"))
	}))
	defer srv.Close()

	n := New(Config{WebhookURL: srv.URL, Enabled: true}, "")
	err := n.Send(context.Background(), Event{Kind: KindUpdated, Text: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "bad payload") {
		t.Errorf("error %q does not contain response body snippet", err.Error())
	}
}

func withRetryDelay(d time.Duration) func() {
	orig := notifyRetryDelay
	notifyRetryDelay = d
	return func() { notifyRetryDelay = orig }
}

func TestNotifyRetriesWebhookOn5xxThenSucceeds(t *testing.T) {
	defer withRetryDelay(time.Millisecond)()

	var count int32
	second := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&count, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		close(second)
	}))
	defer srv.Close()

	n := New(Config{WebhookURL: srv.URL, Enabled: true}, "")
	n.Notify(Event{Kind: KindUpdated, Text: "hi"})

	select {
	case <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook was not retried after 500")
	}
	if got := atomic.LoadInt32(&count); got != 2 {
		t.Errorf("request count = %d, want 2", got)
	}
}

func TestNotifyDoesNotRetryWebhookOn4xx(t *testing.T) {
	defer withRetryDelay(time.Millisecond)()

	var count int32
	hits := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusBadRequest)
		hits <- struct{}{}
	}))
	defer srv.Close()

	n := New(Config{WebhookURL: srv.URL, Enabled: true}, "")
	n.Notify(Event{Kind: KindUpdated, Text: "hi"})

	select {
	case <-hits:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook was not called")
	}
	// 4xx не повторяется, до паузы sendWithRetry не доходит.
	// Короткого окна хватит, чтобы поймать лишний второй запрос
	select {
	case <-hits:
		t.Fatal("webhook was retried after 400")
	case <-time.After(50 * time.Millisecond):
	}
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("request count = %d, want 1", got)
	}
}

func TestNotifyRetriesOnlyTheFailingTransport(t *testing.T) {
	defer withRetryDelay(time.Millisecond)()

	origAPI := telegramAPIURL
	defer func() { telegramAPIURL = origAPI }()

	telegramHits := make(chan struct{}, 4)
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		telegramHits <- struct{}{}
	}))
	defer tgSrv.Close()
	telegramAPIURL = tgSrv.URL + "/bot%s/sendMessage"

	// слушатель принимает и сразу закрывает соединение: настоящая сетевая ошибка
	// без HTTP-статуса, и реальная сеть не нужна
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	webhookAccepts := make(chan struct{}, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			webhookAccepts <- struct{}{}
			_ = c.Close()
		}
	}()

	n := New(Config{
		TelegramToken:  "tok",
		TelegramChatID: "1",
		WebhookURL:     "http://" + ln.Addr().String(),
		Enabled:        true,
	}, "")
	n.Notify(Event{Kind: KindUpdated, Text: "hi"})

	for i := 0; i < 2; i++ {
		select {
		case <-webhookAccepts:
		case <-time.After(2 * time.Second):
			t.Fatalf("webhook attempt %d did not arrive", i+1)
		}
	}
	select {
	case <-webhookAccepts:
		t.Fatal("webhook was retried more than once")
	case <-time.After(50 * time.Millisecond):
	}

	select {
	case <-telegramHits:
	case <-time.After(2 * time.Second):
		t.Fatal("telegram was not called")
	}
	select {
	case <-telegramHits:
		t.Fatal("telegram was retried after 4xx")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestSendDoesNotRetry(t *testing.T) {
	defer withRetryDelay(time.Millisecond)()

	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := New(Config{WebhookURL: srv.URL, Enabled: true}, "")
	if err := n.Send(context.Background(), Event{Kind: KindUpdated, Text: "hi"}); err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("request count = %d, want 1 (Send must not retry)", got)
	}
}

func TestSendTelegramUsesPhotoWhenImageSet(t *testing.T) {
	origPhoto, origMsg := telegramPhotoURL, telegramAPIURL
	defer func() { telegramPhotoURL, telegramAPIURL = origPhoto, origMsg }()

	var paths []string
	var body struct {
		Photo   string `json:"photo"`
		Caption string `json:"caption"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	telegramPhotoURL = srv.URL + "/bot%s/sendPhoto"
	telegramAPIURL = srv.URL + "/bot%s/sendMessage"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true}, "")
	ev := UpdatedEvent("Some.Release", "https://example.com/t1", "http://127.0.0.1:1/poster.jpg")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/sendPhoto") {
		t.Fatalf("requests = %v, want a single sendPhoto", paths)
	}
	if body.Photo != "http://127.0.0.1:1/poster.jpg" {
		t.Errorf("photo = %q", body.Photo)
	}
	if !strings.Contains(body.Caption, "Some.Release") || !strings.Contains(body.Caption, "https://example.com/t1") {
		t.Errorf("caption %q lost the release name or link", body.Caption)
	}
}

func TestSendTelegramFallsBackToMessageWhenPhotoRejected(t *testing.T) {
	origPhoto, origMsg := telegramPhotoURL, telegramAPIURL
	defer func() { telegramPhotoURL, telegramAPIURL = origPhoto, origMsg }()

	var paths []string
	var text string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if strings.HasSuffix(r.URL.Path, "/sendPhoto") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"ok":false,"description":"wrong file identifier"}`))
			return
		}
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		text = body.Text
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	telegramPhotoURL = srv.URL + "/bot%s/sendPhoto"
	telegramAPIURL = srv.URL + "/bot%s/sendMessage"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true}, "")
	ev := UpdatedEvent("Some.Release", "https://example.com/t1", "http://127.0.0.1:1/dead.jpg")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatalf("photo failure must not fail the whole send: %v", err)
	}
	if len(paths) != 2 || !strings.HasSuffix(paths[1], "/sendMessage") {
		t.Fatalf("requests = %v, want sendPhoto then sendMessage", paths)
	}
	if !strings.Contains(text, "Some.Release") {
		t.Errorf("fallback text %q lost the release name", text)
	}
}

func TestTelegramCaptionTruncatedToLimit(t *testing.T) {
	origPhoto, origMsg := telegramPhotoURL, telegramAPIURL
	defer func() { telegramPhotoURL, telegramAPIURL = origPhoto, origMsg }()
	telegramAPIURL = "http://127.0.0.1:1/bot%s/sendMessage"

	var caption string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Caption string `json:"caption"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		caption = body.Caption
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	telegramPhotoURL = srv.URL + "/bot%s/sendPhoto"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true}, "")
	ev := Event{Kind: KindUpdated, Text: strings.Repeat("я", 2000), ImageURL: "http://127.0.0.1:1/poster.jpg"}
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(caption)); got != maxCaption {
		t.Errorf("caption length = %d runes, want %d", got, maxCaption)
	}
}

func TestSendTelegramUploadsImageBytes(t *testing.T) {
	origPhoto, origMsg := telegramPhotoURL, telegramAPIURL
	defer func() { telegramPhotoURL, telegramAPIURL = origPhoto, origMsg }()
	telegramAPIURL = "http://127.0.0.1:1/bot%s/sendMessage"

	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPEGBYTES"))
	}))
	defer img.Close()

	var gotFile, gotCaption, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotCaption = r.FormValue("caption")
		f, _, err := r.FormFile("photo")
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer func() { _ = f.Close() }()
		b, _ := io.ReadAll(f)
		gotFile = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	telegramPhotoURL = srv.URL + "/bot%s/sendPhoto"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true}, "")
	n.posterHC = img.Client()
	ev := UpdatedEvent("Some.Release", "https://example.com/t1", img.URL+"/poster.jpg")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if gotFile != "JPEGBYTES" {
		t.Errorf("uploaded file = %q, want the fetched image bytes", gotFile)
	}
	if !strings.HasPrefix(gotContentType, "multipart/form-data") {
		t.Errorf("content-type = %q, want multipart", gotContentType)
	}
	if !strings.Contains(gotCaption, "Some.Release") {
		t.Errorf("caption %q lost the release name", gotCaption)
	}
}

func TestTelegramPosterOnLocalAddressNotFetched(t *testing.T) {
	origPhoto, origMsg := telegramPhotoURL, telegramAPIURL
	defer func() { telegramPhotoURL, telegramAPIURL = origPhoto, origMsg }()
	telegramAPIURL = "http://127.0.0.1:1/bot%s/sendMessage"

	var imgHits atomic.Int32
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		imgHits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPEGBYTES"))
	}))
	defer img.Close()

	var gotContentType string
	var body struct {
		Photo string `json:"photo"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	telegramPhotoURL = srv.URL + "/bot%s/sendPhoto"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true}, "")
	ev := UpdatedEvent("Some.Release", "https://example.com/t1", img.URL+"/poster.jpg")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if hits := imgHits.Load(); hits != 0 {
		t.Errorf("poster server on a loopback address got %d requests, want 0", hits)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Errorf("content-type = %q, want a JSON photo link, not an upload", gotContentType)
	}
	if body.Photo != img.URL+"/poster.jpg" {
		t.Errorf("photo = %q, want the original link", body.Photo)
	}
}

func fakePosterLookup(t *testing.T, ips map[string]string) {
	t.Helper()
	orig := lookupPosterHost
	t.Cleanup(func() { lookupPosterHost = orig })
	lookupPosterHost = func(_ context.Context, host string) ([]netip.Addr, error) {
		ip, ok := ips[host]
		if !ok {
			return nil, errors.New("no such host " + host)
		}
		return []netip.Addr{netip.MustParseAddr(ip)}, nil
	}
}

func TestPosterClientProxyModeRefusesNameResolvingToLocal(t *testing.T) {
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPEGBYTES"))
	}))
	defer proxy.Close()
	fakePosterLookup(t, map[string]string{"lan.example.invalid": "192.168.1.1"})

	res, err := newPosterClient(proxy.URL).Get("http://lan.example.invalid/p.jpg")
	if err == nil {
		_ = res.Body.Close()
		t.Fatal("GET succeeded, want a refusal for a name resolving to a private address")
	}
	if hits.Load() != 0 {
		t.Errorf("proxy got %d requests, want 0", hits.Load())
	}
}

func TestPosterClientProxyModeRefusesProxyAddress(t *testing.T) {
	fakePosterLookup(t, map[string]string{"proxy.example.invalid": "93.184.216.34"})

	res, err := newPosterClient("http://proxy.example.invalid:3128").Get("http://proxy.example.invalid:3128/p.jpg")
	if err == nil {
		_ = res.Body.Close()
		t.Fatal("GET succeeded, want a refusal for a poster on the proxy address")
	}
	if !strings.Contains(err.Error(), "proxy address") {
		t.Errorf("error = %q, want the proxy address refusal", err)
	}
}

func TestPosterClientUsesProxy(t *testing.T) {
	fakePosterLookup(t, map[string]string{"poster.example.invalid": "93.184.216.34"})
	var gotURL string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPEGBYTES"))
	}))
	defer proxy.Close()

	hc := newPosterClient(proxy.URL)
	res, err := hc.Get("http://poster.example.invalid/p.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "image/jpeg" {
		t.Errorf("status %d, content-type %q, want 200 image/jpeg", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if gotURL != "http://poster.example.invalid/p.jpg" {
		t.Errorf("proxy got %q, want the absolute poster URL", gotURL)
	}
}

func TestPosterClientRefusesLoopbackDial(t *testing.T) {
	var hits atomic.Int32
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer local.Close()

	hc := newPosterClient("")
	res, err := hc.Get(local.URL)
	if err == nil {
		_ = res.Body.Close()
		t.Fatal("GET to a loopback address succeeded, want a refused dial")
	}
	if hits.Load() != 0 {
		t.Errorf("loopback server got %d requests, want 0", hits.Load())
	}
}

func TestSendTelegramFallsBackToURLWhenImageNotFetchable(t *testing.T) {
	origPhoto, origMsg := telegramPhotoURL, telegramAPIURL
	defer func() { telegramPhotoURL, telegramAPIURL = origPhoto, origMsg }()
	telegramAPIURL = "http://127.0.0.1:1/bot%s/sendMessage"

	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer img.Close()

	var gotPhoto string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Photo string `json:"photo"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotPhoto = body.Photo
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	telegramPhotoURL = srv.URL + "/bot%s/sendPhoto"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true}, "")
	n.posterHC = img.Client()
	ev := UpdatedEvent("Some.Release", "https://example.com/t1", img.URL+"/poster.jpg")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if gotPhoto != img.URL+"/poster.jpg" {
		t.Errorf("photo = %q, want the original link so telegram fetches it itself", gotPhoto)
	}
}

func TestTelegramMessageLinksSubjectInsteadOfBareURL(t *testing.T) {
	orig := telegramAPIURL
	defer func() { telegramAPIURL = orig }()

	var body struct {
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	telegramAPIURL = srv.URL + "/bot%s/sendMessage"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true}, "")
	ev := UpdatedEvent("Some.Release.2026", "https://example.com/forum/viewtopic.php?t=1", "")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	want := `<a href="https://example.com/forum/viewtopic.php?t=1">Some.Release.2026</a>`
	if !strings.Contains(body.Text, want) {
		t.Errorf("text %q does not carry the linked name %q", body.Text, want)
	}
	if body.ParseMode != "HTML" {
		t.Errorf("parse_mode = %q, want HTML", body.ParseMode)
	}
	if strings.Contains(body.Text, "\nhttps://example.com/forum/viewtopic.php?t=1") {
		t.Errorf("text %q still repeats the link on its own line", body.Text)
	}
}

func TestTelegramMessageEscapesNameForHTML(t *testing.T) {
	orig := telegramAPIURL
	defer func() { telegramAPIURL = orig }()

	var body struct {
		Text string `json:"text"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	telegramAPIURL = srv.URL + "/bot%s/sendMessage"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true}, "")
	ev := UpdatedEvent("Release <b> & (2026) [x_264]", "https://example.com/t?a=1&b=2", "")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body.Text, "<b>") {
		t.Errorf("text %q leaks raw markup from the release name", body.Text)
	}
	if !strings.Contains(body.Text, "(2026) [x_264]") {
		t.Errorf("text %q mangled brackets and underscores", body.Text)
	}
	if !strings.Contains(body.Text, "https://example.com/t?a=1&amp;b=2") {
		t.Errorf("text %q did not escape the ampersand in the link", body.Text)
	}
}

func TestTelegramEventsWithoutSubjectStayPlain(t *testing.T) {
	orig := telegramAPIURL
	defer func() { telegramAPIURL = orig }()

	var body struct {
		Text      string `json:"text"`
		ParseMode string `json:"parse_mode"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	telegramAPIURL = srv.URL + "/bot%s/sendMessage"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true}, "")
	if err := n.Send(context.Background(), TrackerDownEvent("rutracker", "connection refused")); err != nil {
		t.Fatal(err)
	}
	if body.ParseMode != "" {
		t.Errorf("parse_mode = %q, want none for a message without a link", body.ParseMode)
	}
	if !strings.Contains(body.Text, "connection refused") {
		t.Errorf("text %q lost the error message", body.Text)
	}
}

func TestTelegramCaptionTrimsNameNotMarkup(t *testing.T) {
	origPhoto, origMsg := telegramPhotoURL, telegramAPIURL
	defer func() { telegramPhotoURL, telegramAPIURL = origPhoto, origMsg }()
	telegramAPIURL = "http://127.0.0.1:1/bot%s/sendMessage"

	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPEGBYTES"))
	}))
	defer img.Close()

	var caption, parseMode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		caption = r.FormValue("caption")
		parseMode = r.FormValue("parse_mode")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	telegramPhotoURL = srv.URL + "/bot%s/sendPhoto"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true}, "")
	n.posterHC = img.Client()
	ev := UpdatedEvent(strings.Repeat("я", 2000), "https://example.com/t1", img.URL+"/poster.jpg")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if parseMode != "HTML" {
		t.Errorf("parse_mode = %q, want HTML", parseMode)
	}
	if !strings.HasSuffix(caption, "</a>") {
		t.Errorf("caption %q was cut inside the link markup", caption)
	}
	// в caption у telegram считается видимый текст, а не разметка
	visible := len([]rune(stripTags(caption)))
	if visible > maxCaption {
		t.Errorf("visible caption length = %d runes, want at most %d", visible, maxCaption)
	}
}

func stripTags(s string) string {
	for {
		open := strings.Index(s, "<")
		if open < 0 {
			return s
		}
		closed := strings.Index(s[open:], ">")
		if closed < 0 {
			return s
		}
		s = s[:open] + s[open+closed+1:]
	}
}

func TestErrorsHideTelegramToken(t *testing.T) {
	const token = "123456:AAsecret-token"
	orig := telegramGetUpdatesURL
	defer func() { telegramGetUpdatesURL = orig }()
	// закрытый сервер: *url.Error печатает URL целиком, вместе с токеном в пути
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	telegramGetUpdatesURL = srv.URL + "/bot%s/getUpdates"
	srv.Close()

	_, err := ResolveChatID(context.Background(), token, "")
	if err == nil {
		t.Fatal("want error from closed server")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("token leaked into error: %s", err)
	}

	n := New(Config{Enabled: true, TelegramToken: token, TelegramChatID: "1"}, "")
	origAPI := telegramAPIURL
	defer func() { telegramAPIURL = origAPI }()
	telegramAPIURL = srv.URL + "/bot%s/sendMessage"
	if err = n.Send(context.Background(), Event{Kind: KindTest, Text: "x"}); err == nil {
		t.Fatal("want error from closed server")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("token leaked into error: %s", err)
	}
}

func countingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func waitHits(t *testing.T, hits *atomic.Int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("got %d hits, want %d", hits.Load(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNotifyRoutesByChannelEvents(t *testing.T) {
	origAPI := telegramAPIURL
	defer func() { telegramAPIURL = origAPI }()

	tg, tgHits := countingServer(t)
	wh, whHits := countingServer(t)
	telegramAPIURL = tg.URL + "/bot%s/sendMessage"

	n := New(Config{
		TelegramToken:  "tok",
		TelegramChatID: "1",
		TelegramEvents: []string{KindUpdated},
		WebhookURL:     wh.URL,
		Enabled:        true,
	}, "")
	n.Notify(TrackerDownEvent("rutracker", "boom"))
	waitHits(t, whHits, 1)
	time.Sleep(100 * time.Millisecond)
	if tgHits.Load() != 0 {
		t.Fatalf("telegram subscribed to updated only got tracker_down: %d hits", tgHits.Load())
	}

	n = New(Config{
		TelegramToken:  "tok",
		TelegramChatID: "1",
		TelegramEvents: []string{KindTrackerDown},
		WebhookURL:     wh.URL,
		WebhookEvents:  []string{},
		Enabled:        true,
	}, "")
	n.Notify(TrackerDownEvent("rutracker", "boom"))
	waitHits(t, tgHits, 1)
	time.Sleep(100 * time.Millisecond)
	if whHits.Load() != 1 {
		t.Fatalf("webhook with empty list must stay silent, hits %d", whHits.Load())
	}
}

func TestNotifySlowChannelDoesNotDelayOthers(t *testing.T) {
	origAPI := telegramAPIURL
	defer func() { telegramAPIURL = origAPI }()

	release := make(chan struct{})
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer tg.Close()
	defer close(release)
	telegramAPIURL = tg.URL + "/bot%s/sendMessage"

	wh, whHits := countingServer(t)
	n := New(Config{
		TelegramToken:  "tok",
		TelegramChatID: "1",
		WebhookURL:     wh.URL,
		Enabled:        true,
	}, "")
	n.Notify(Event{Kind: KindUpdated, Text: "hi"})
	waitHits(t, whHits, 1)
}

func TestNotifyCheckFailedNotInDefault(t *testing.T) {
	for _, k := range DefaultEvents() {
		if k == KindCheckFailed {
			t.Fatal("check_failed must be off by default")
		}
	}
	if !KnownEvent(KindCheckFailed) || !KnownEvent(KindMirrorSwitched) || KnownEvent(KindTest) || KnownEvent("nope") {
		t.Fatal("KnownEvent must accept check_failed and mirror_switched and reject test and unknown kinds")
	}
	ev := CheckFailedEvent("Some.Release", "https://example.com/t1", "timeout")
	if ev.Kind != KindCheckFailed || !strings.Contains(ev.plain(), "Some.Release") || !strings.Contains(ev.plain(), "timeout") || ev.URL == "" {
		t.Fatalf("unexpected check_failed event: %+v", ev)
	}

	wh, whHits := countingServer(t)
	New(Config{WebhookURL: wh.URL, Enabled: true}, "").Notify(ev)
	time.Sleep(100 * time.Millisecond)
	if whHits.Load() != 0 {
		t.Fatal("check_failed reached a channel with no stored list")
	}
	New(Config{WebhookURL: wh.URL, WebhookEvents: []string{KindCheckFailed}, Enabled: true}, "").Notify(ev)
	waitHits(t, whHits, 1)
}

func TestSendIgnoresEventLists(t *testing.T) {
	wh, whHits := countingServer(t)
	n := New(Config{WebhookURL: wh.URL, WebhookEvents: []string{}, Enabled: true}, "")
	if err := n.Send(context.Background(), Event{Kind: KindTest, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if whHits.Load() != 1 {
		t.Fatalf("test event must bypass lists, hits %d", whHits.Load())
	}
}

func TestMirrorSwitchedEvent(t *testing.T) {
	ev := MirrorSwitchedEvent("rutracker", "rutracker.org", "rutracker.nl")
	if ev.Kind != "mirror_switched" {
		t.Errorf("kind = %q, want mirror_switched", ev.Kind)
	}
	for _, want := range []string{"rutracker", "rutracker.org", "rutracker.nl"} {
		if !strings.Contains(ev.plain(), want) {
			t.Errorf("text %q does not contain %q", ev.plain(), want)
		}
	}
}

func TestNotifyCountsNotifications(t *testing.T) {
	var status atomic.Int32
	status.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()

	waitFor := func(result string, want uint64) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for metrics.Notifications.Value("webhook", result) != want {
			if time.Now().After(deadline) {
				t.Fatalf("webhook %s = %d, want %d", result, metrics.Notifications.Value("webhook", result), want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	okBefore := metrics.Notifications.Value("webhook", "ok")
	errBefore := metrics.Notifications.Value("webhook", "error")

	n := New(Config{WebhookURL: srv.URL, Enabled: true}, "")
	if err := n.Send(context.Background(), Event{Kind: KindUpdated, Text: "test"}); err != nil {
		t.Fatal(err)
	}
	n.Notify(Event{Kind: KindUpdated, Text: "hi"})
	waitFor("ok", okBefore+1)

	status.Store(http.StatusBadRequest)
	n.Notify(Event{Kind: KindUpdated, Text: "hi"})
	waitFor("error", errBefore+1)
	if got := metrics.Notifications.Value("webhook", "ok"); got != okBefore+1 {
		t.Fatalf("ok = %d, want %d (Send must not count)", got, okBefore+1)
	}
}

func TestErrorsHideWebhookURLSecrets(t *testing.T) {
	defer withRetryDelay(time.Millisecond)()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	host := strings.TrimPrefix(srv.URL, "http://")
	srv.Close()

	n := New(Config{WebhookURL: "http://wu:SEC-wp@" + host + "/SEC-wpath?t=SEC-wq", Enabled: true}, "")
	err := n.Send(context.Background(), Event{Kind: KindTest, Text: "x"})
	if err == nil {
		t.Fatal("want error from closed server")
	}
	if strings.Contains(err.Error(), "SEC-") {
		t.Fatalf("secret leaked into error: %s", err)
	}
	if !strings.Contains(err.Error(), host) {
		t.Fatalf("host %s missing from error: %s", host, err)
	}

	n.Notify(Event{Kind: KindTest, Text: "x"})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range logger.Snapshot(100) {
			if !strings.Contains(e.Message, "notify: webhook") || !strings.Contains(e.Message, host) {
				continue
			}
			if strings.Contains(e.Message, "SEC-") {
				t.Fatalf("secret leaked into log: %s", e.Message)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no notify: webhook log entry")
}

func TestMirrorSuggestedEvents(t *testing.T) {
	if !KnownEvent(KindMirrorSuggested) || KindMirrorSuggested != "mirror_suggested" {
		t.Fatal("mirror_suggested must be a known event kind")
	}
	if !slices.Contains(DefaultEvents(), KindMirrorSuggested) {
		t.Fatalf("mirror_suggested must be in the default events: %v", DefaultEvents())
	}
	digest := MirrorDigestEvent([]MirrorDigestItem{{Tracker: "a", Kind: DigestConfirm, Host: "a.x"}, {Tracker: "b", Kind: DigestRemove, Host: "b.x"}})
	if digest.Kind != KindMirrorSuggested || digest.Text != "Tracker mirrors: a: new mirror a.x awaits confirmation; b: remove b.x" {
		t.Fatalf("digest = %+v", digest)
	}
	paused := MirrorPausedEvent("kinozal", "kinozal.xyz")
	want := "Tracker kinozal is paused: only the new mirror kinozal.xyz responds. Confirm it in the tracker settings."
	if paused.Kind != KindMirrorSuggested || paused.Text != want {
		t.Fatalf("paused = %+v", paused)
	}
}

func TestNotifySkipsDisabledChannel(t *testing.T) {
	wh, whHits := countingServer(t)
	n := New(Config{WebhookURL: wh.URL, WebhookDisabled: true, Enabled: true}, "")
	n.Notify(Event{Kind: KindUpdated, Text: "x"})
	time.Sleep(100 * time.Millisecond)
	if whHits.Load() != 0 {
		t.Fatalf("disabled webhook got %d hits", whHits.Load())
	}

	n = New(Config{WebhookURL: wh.URL, Enabled: true}, "")
	n.Notify(Event{Kind: KindUpdated, Text: "x"})
	waitHits(t, whHits, 1)
}

func TestSendIgnoresChannelDisabled(t *testing.T) {
	wh, whHits := countingServer(t)
	n := New(Config{WebhookURL: wh.URL, WebhookDisabled: true, Enabled: true}, "")
	if err := n.Send(context.Background(), Event{Kind: KindTest, Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if whHits.Load() != 1 {
		t.Fatalf("test message must reach a disabled channel, hits %d", whHits.Load())
	}
}

type socksConnect struct {
	atyp byte
	host string
	port int
}

func fakeSocksServer(t *testing.T, accept ...string) (addr string, connects func() []socksConnect) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var got []socksConnect
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				hdr := make([]byte, 2)
				if _, err := io.ReadFull(c, hdr); err != nil {
					return
				}
				if _, err := io.ReadFull(c, make([]byte, hdr[1])); err != nil {
					return
				}
				_, _ = c.Write([]byte{5, 0})
				req := make([]byte, 4)
				if _, err := io.ReadFull(c, req); err != nil {
					return
				}
				var host string
				switch req[3] {
				case 1:
					ip := make([]byte, 4)
					_, _ = io.ReadFull(c, ip)
					host = net.IP(ip).String()
				case 3:
					l := make([]byte, 1)
					_, _ = io.ReadFull(c, l)
					name := make([]byte, l[0])
					_, _ = io.ReadFull(c, name)
					host = string(name)
				case 4:
					ip := make([]byte, 16)
					_, _ = io.ReadFull(c, ip)
					host = net.IP(ip).String()
				}
				p := make([]byte, 2)
				_, _ = io.ReadFull(c, p)
				mu.Lock()
				got = append(got, socksConnect{atyp: req[3], host: host, port: int(p[0])<<8 | int(p[1])})
				mu.Unlock()
				if slices.Contains(accept, host) {
					_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
			}()
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().String(), func() []socksConnect {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

func TestPosterClientSocksSendsResolvedIP(t *testing.T) {
	fakePosterLookup(t, map[string]string{"poster.example.invalid": "93.184.216.34"})
	addr, connects := fakeSocksServer(t)

	res, err := newPosterClient("socks5://" + addr).Get("http://poster.example.invalid/p.jpg")
	if err == nil {
		_ = res.Body.Close()
	}
	got := connects()
	if len(got) != 1 {
		t.Fatalf("socks server got %d CONNECT requests (GET err %v), want 1", len(got), err)
	}
	if got[0].atyp != 1 || got[0].host != "93.184.216.34" || got[0].port != 80 {
		t.Errorf("CONNECT = %+v, want IPv4 93.184.216.34:80", got[0])
	}
}

func TestPosterClientSocksRefusesNameResolvingToLocal(t *testing.T) {
	fakePosterLookup(t, map[string]string{"lan.example.invalid": "10.0.0.5"})
	addr, connects := fakeSocksServer(t)

	res, err := newPosterClient("socks5://" + addr).Get("http://lan.example.invalid/p.jpg")
	if err == nil {
		_ = res.Body.Close()
		t.Fatal("GET succeeded, want a refusal for a name resolving to a private address")
	}
	if n := len(connects()); n != 0 {
		t.Errorf("socks server got %d CONNECT requests, want 0", n)
	}
}

func TestPosterClientIgnoresEnvProxy(t *testing.T) {
	hc := newPosterClient("")
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", hc.Transport)
	}
	if tr.Proxy != nil {
		t.Error("poster transport has a Proxy func, want nil so HTTP(S)_PROXY is never used")
	}
}

func TestPublicAddrRefusesCGNATAndNAT64Private(t *testing.T) {
	for _, s := range []string{"100.64.0.1", "100.127.255.254", "64:ff9b::a00:1", "64:ff9b:1::5db8:d822"} {
		if err := publicAddr(netip.MustParseAddr(s)); err == nil {
			t.Errorf("publicAddr(%s) = nil, want a refusal", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "64:ff9b::5db8:d822", "100.128.0.1"} {
		if err := publicAddr(netip.MustParseAddr(s)); err != nil {
			t.Errorf("publicAddr(%s) = %v, want nil", s, err)
		}
	}
}

func TestPosterClientSocksTriesNextAnswer(t *testing.T) {
	orig := lookupPosterHost
	t.Cleanup(func() { lookupPosterHost = orig })
	lookupPosterHost = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("93.184.216.35")}, nil
	}
	addr, connects := fakeSocksServer(t, "93.184.216.35")

	res, err := newPosterClient("socks5://" + addr).Get("http://poster.example.invalid/p.jpg")
	if err == nil {
		_ = res.Body.Close()
	}
	got := connects()
	if len(got) < 2 || got[0].host != "93.184.216.34" || got[1].host != "93.184.216.35" {
		t.Errorf("CONNECT requests = %+v (GET err %v), want the first answer refused, then the second tried", got, err)
	}
}
