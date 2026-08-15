package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
