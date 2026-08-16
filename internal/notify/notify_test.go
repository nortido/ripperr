package notify

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventTextsDistinguishTypesAndCarrySubject(t *testing.T) {
	cases := []struct {
		name    string
		ev      Event
		wantKw  string
		wantSub string
	}{
		{"updated", UpdatedEvent("Some.Release.2026", "https://example.com/t1"), "qBittorrent", "Some.Release.2026"},
		{"broken", BrokenEvent("Broken.Release", "https://example.com/t2", 5), "игнор", "Broken.Release"},
		{"tracker down", TrackerDownEvent("rutracker", "connection refused"), "недоступен", "rutracker"},
		{"tracker up", TrackerUpEvent("rutracker"), "возобновлены", "rutracker"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(c.ev.Text, c.wantKw) {
				t.Errorf("text %q does not contain distinguishing keyword %q", c.ev.Text, c.wantKw)
			}
			if !strings.Contains(c.ev.Text, c.wantSub) {
				t.Errorf("text %q does not contain subject %q", c.ev.Text, c.wantSub)
			}
			for _, other := range cases {
				if other.name == c.name {
					continue
				}
				if strings.Contains(other.ev.Text, c.wantKw) {
					t.Errorf("keyword %q meant to distinguish %q also appears in %q text", c.wantKw, c.name, other.name)
				}
			}
		})
	}
	if BrokenEvent("x", "", 5).Text == "" || !strings.Contains(BrokenEvent("x", "", 3).Text, "3") {
		t.Error("broken event text does not carry the streak count")
	}
}

func TestTrackerDownEventCarriesErrorText(t *testing.T) {
	ev := TrackerDownEvent("rutracker", "connection refused")
	if !strings.Contains(ev.Text, "connection refused") {
		t.Errorf("text %q does not contain the error message", ev.Text)
	}
}

func TestHasTransport(t *testing.T) {
	if New(Config{}).HasTransport() {
		t.Error("empty config should have no transport")
	}
	if New(Config{TelegramToken: "tok"}).HasTransport() {
		t.Error("token without chat id should have no transport")
	}
	if !New(Config{TelegramToken: "tok", TelegramChatID: "1"}).HasTransport() {
		t.Error("token+chat id should have a transport")
	}
	if !New(Config{WebhookURL: "http://example.com"}).HasTransport() {
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

	n := New(Config{WebhookURL: srv.URL, Enabled: true})
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

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Enabled: true})
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

	n := New(Config{WebhookURL: srv.URL, Enabled: false})
	n.Notify(Event{Kind: KindUpdated, Text: "should not send"})

	// wants() is checked synchronously before Notify ever spawns its delivery
	// goroutine, so a non-blocking read proves nothing was scheduled - no
	// sleep needed to "wait and see".
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

	n := New(Config{WebhookURL: srv.URL, Enabled: true})
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

	got, err := ResolveChatID(context.Background(), "tok")
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

	_, err := ResolveChatID(context.Background(), "tok")
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

	_, err := ResolveChatID(context.Background(), "tok")
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

	_, err := ResolveChatID(context.Background(), "tok")
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

	n := New(Config{WebhookURL: srv.URL, Enabled: true})
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

	n := New(Config{WebhookURL: srv.URL, Enabled: true})
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

	n := New(Config{WebhookURL: srv.URL, Enabled: true})
	n.Notify(Event{Kind: KindUpdated, Text: "hi"})

	select {
	case <-hits:
	case <-time.After(2 * time.Second):
		t.Fatal("webhook was not called")
	}
	// 4xx is not retryable, so sendWithRetry never reaches the delay branch;
	// a short guard window is enough to catch an errant second request
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

	// a listener that accepts and immediately closes the connection makes the
	// webhook client fail with a genuine network error (no HTTP status at
	// all), without ever touching the real network
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
	})
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

	n := New(Config{WebhookURL: srv.URL, Enabled: true})
	if err := n.Send(context.Background(), Event{Kind: KindUpdated, Text: "hi"}); err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("request count = %d, want 1 (Send must not retry)", got)
	}
}
