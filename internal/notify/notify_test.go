package notify

import (
	"context"
	"encoding/json"
	"io"
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
		{"updated", UpdatedEvent("Some.Release.2026", "https://example.com/t1", ""), "qBittorrent", "Some.Release.2026"},
		{"broken", BrokenEvent("Broken.Release", "https://example.com/t2", 5), "игнор", "Broken.Release"},
		{"tracker down", TrackerDownEvent("rutracker", "connection refused"), "недоступен", "rutracker"},
		{"tracker up", TrackerUpEvent("rutracker"), "возобновлены", "rutracker"},
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

	// wants() проверяется синхронно ещё до того, как Notify запустит горутину
	// доставки, поэтому неблокирующее чтение доказывает, что ничего не запланировано,
	// спать и ждать не нужно.
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
	// 4xx is not retryable, so sendWithRetry never reaches the delay branch;
	// короткого окна хватает, чтобы поймать ошибочный второй запрос
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

	// слушатель, который принимает и тут же закрывает соединение, роняет
	// клиент вебхука настоящей сетевой ошибкой (вообще без HTTP-статуса),
	// не трогая при этом реальную сеть
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
