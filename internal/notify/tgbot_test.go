package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ripperr/internal/system/logger"
)

const botToken = "123456:AAbotsecret"

type tgBotServer struct {
	mu       sync.Mutex
	batches  []string
	polls    []map[string]any
	answers  []map[string]any
	edits    []map[string]any
	status   int
	statuses []int
}

func (s *tgBotServer) snapshot() (polls, answers, edits int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.polls), len(s.answers), len(s.edits)
}

func newTgBot(t *testing.T, batches ...string) *tgBotServer {
	t.Helper()
	s := &tgBotServer{batches: batches}
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			s.polls = append(s.polls, body)
			n := len(s.polls) - 1
			status := s.status
			if n < len(s.statuses) && s.statuses[n] != 0 {
				status = s.statuses[n]
			}
			var batch string
			if n < len(s.batches) {
				batch = s.batches[n]
			}
			s.mu.Unlock()
			if status != 0 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"ok":false,"description":"Conflict: terminated by other getUpdates request"}`))
				return
			}
			if batch == "" {
				select {
				case <-r.Context().Done():
				case <-done:
				}
				return
			}
			_, _ = w.Write([]byte(batch))
			return
		case strings.HasSuffix(r.URL.Path, "/answerCallbackQuery"):
			s.answers = append(s.answers, body)
		case strings.HasSuffix(r.URL.Path, "/editMessageReplyMarkup"):
			s.edits = append(s.edits, body)
		}
		s.mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	t.Cleanup(func() { close(done); srv.Close() })
	origPoll, origAnswer, origEdit := telegramGetUpdatesURL, telegramAnswerURL, telegramEditMarkupURL
	origSecs, origMin, origMax := telegramPollSeconds, telegramBackoffMin, telegramBackoffMax
	telegramGetUpdatesURL = srv.URL + "/bot%s/getUpdates"
	telegramAnswerURL = srv.URL + "/bot%s/answerCallbackQuery"
	telegramEditMarkupURL = srv.URL + "/bot%s/editMessageReplyMarkup"
	telegramPollSeconds, telegramBackoffMin, telegramBackoffMax = 7, time.Millisecond, 4*time.Millisecond
	t.Cleanup(func() {
		telegramGetUpdatesURL, telegramAnswerURL, telegramEditMarkupURL = origPoll, origAnswer, origEdit
		telegramPollSeconds, telegramBackoffMin, telegramBackoffMax = origSecs, origMin, origMax
	})
	return s
}

func startButtons(t *testing.T, n *Notifier) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		n.RunTelegramButtons(ctx)
		close(done)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("RunTelegramButtons did not return after ctx cancel")
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func tgBotNotifier(chat string, m *TelegramMutes) *Notifier {
	return New(Config{TelegramToken: botToken, TelegramChatID: chat, Enabled: true, Language: "ru"}, "").WithTelegramMutes(m)
}

func callbackBatch(updateID int, data, chatJSON string) string {
	return `{"ok":true,"result":[{"update_id":` + itoa(updateID) + `,"callback_query":{"id":"cb1","data":"` + data + `","message":{"message_id":9,"chat":` + chatJSON +
		`,"reply_markup":{"inline_keyboard":[[{"text":"Тема","url":"https://t/1"},{"text":"🔕","callback_data":"mute:123"}]]}}}}]}`
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestTelegramButtonPressMutesAnswersAndFlips(t *testing.T) {
	ctx := context.Background()
	bot := newTgBot(t, callbackBatch(100, "mute:123", `{"id":42}`))
	m, _ := newMemMutes()
	stop := startButtons(t, tgBotNotifier("42", m))

	waitUntil(t, "answer, edit and next poll", func() bool {
		p, a, e := bot.snapshot()
		return p >= 2 && a >= 1 && e >= 1
	})
	bot.mu.Lock()
	defer bot.mu.Unlock()
	first := bot.polls[0]
	if first["timeout"] != float64(7) {
		t.Errorf("timeout = %v, want telegramPollSeconds", first["timeout"])
	}
	if got, _ := json.Marshal(first["allowed_updates"]); string(got) != `["message","callback_query"]` {
		t.Errorf("allowed_updates = %s", got)
	}
	if bot.polls[1]["offset"] != float64(101) {
		t.Errorf("next offset = %v, want update_id+1", bot.polls[1]["offset"])
	}
	if muted, _ := m.Muted(ctx, "123"); !muted {
		t.Error("123 not muted after button press")
	}
	if a := bot.answers[0]; a["callback_query_id"] != "cb1" || a["text"] != "Уведомления об этой раздаче в Telegram выключены" {
		t.Errorf("answer = %v", a)
	}
	e := bot.edits[0]
	if e["chat_id"] != float64(42) || e["message_id"] != float64(9) {
		t.Errorf("edit target = %v", e)
	}
	rows := tgButtonsOf(t, e["reply_markup"])
	if len(rows) != 1 || len(rows[0]) != 2 ||
		rows[0][0]["text"] != "Тема" || rows[0][0]["url"] != "https://t/1" ||
		rows[0][1]["text"] != "🔔" || rows[0][1]["callback_data"] != "unmute:123" {
		t.Errorf("flipped markup = %+v", rows)
	}
	stop()
}

func TestTelegramButtonUnmuteFlipsBack(t *testing.T) {
	ctx := context.Background()
	batch := strings.ReplaceAll(callbackBatch(5, "unmute:123", `{"id":42}`), `"🔕","callback_data":"mute:123"`, `"🔔","callback_data":"unmute:123"`)
	bot := newTgBot(t, batch)
	m, _ := newMemMutes()
	if err := m.Set(ctx, "123", true); err != nil {
		t.Fatal(err)
	}
	startButtons(t, tgBotNotifier("42", m))
	waitUntil(t, "edit", func() bool { _, _, e := bot.snapshot(); return e >= 1 })
	if muted, _ := m.Muted(ctx, "123"); muted {
		t.Error("123 still muted after unmute press")
	}
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if bot.answers[0]["text"] != "Уведомления об этой раздаче в Telegram включены" {
		t.Errorf("answer = %v", bot.answers[0])
	}
	rows := tgButtonsOf(t, bot.edits[0]["reply_markup"])
	if rows[0][1]["text"] != "🔕" || rows[0][1]["callback_data"] != "mute:123" {
		t.Errorf("flipped markup = %+v", rows)
	}
}

func TestTelegramButtonForeignChatIgnored(t *testing.T) {
	ctx := context.Background()
	bot := newTgBot(t, callbackBatch(100, "mute:123", `{"id":7}`))
	m, _ := newMemMutes()
	startButtons(t, tgBotNotifier("42", m))
	waitUntil(t, "second poll", func() bool { p, _, _ := bot.snapshot(); return p >= 2 })
	bot.mu.Lock()
	defer bot.mu.Unlock()
	if len(bot.answers) != 1 {
		t.Fatalf("answers = %d, want 1 (spinner must stop)", len(bot.answers))
	}
	if txt, has := bot.answers[0]["text"]; has && txt != "" {
		t.Errorf("foreign chat got toast %v", txt)
	}
	if len(bot.edits) != 0 {
		t.Errorf("edits = %d, want 0", len(bot.edits))
	}
	if muted, _ := m.Muted(ctx, "123"); muted {
		t.Error("foreign chat changed the mute set")
	}
}

func TestTelegramButtonUsernameChatMatchesCaseInsensitively(t *testing.T) {
	ctx := context.Background()
	bot := newTgBot(t, callbackBatch(100, "mute:123", `{"id":-100123,"username":"mychannel"}`))
	m, _ := newMemMutes()
	startButtons(t, tgBotNotifier(" @MyChannel ", m))
	waitUntil(t, "edit", func() bool { _, _, e := bot.snapshot(); return e >= 1 })
	if muted, _ := m.Muted(ctx, "123"); !muted {
		t.Error("@name chat did not match username")
	}
}

func TestTelegramButtonUnknownDataOnlyAnswered(t *testing.T) {
	ctx := context.Background()
	bot := newTgBot(t, callbackBatch(100, "other:123", `{"id":42}`))
	m, _ := newMemMutes()
	startButtons(t, tgBotNotifier("42", m))
	waitUntil(t, "second poll", func() bool { p, _, _ := bot.snapshot(); return p >= 2 })
	_, a, e := bot.snapshot()
	if a != 1 || e != 0 {
		t.Errorf("answers %d edits %d, want 1 and 0", a, e)
	}
	if muted, _ := m.Muted(ctx, "123"); muted {
		t.Error("unknown verb changed the mute set")
	}
}

func countLoggedPrefix(prefix string) int {
	n := 0
	for _, e := range logger.Snapshot(0) {
		if strings.HasPrefix(e.Message, prefix) {
			n++
		}
	}
	return n
}

func TestTelegramButtonsConflictWarnsOnce(t *testing.T) {
	bot := newTgBot(t)
	bot.status = http.StatusConflict
	m, _ := newMemMutes()
	before := countLoggedPrefix("notify: telegram buttons")
	stop := startButtons(t, tgBotNotifier("42", m))
	waitUntil(t, "three polls", func() bool { p, _, _ := bot.snapshot(); return p >= 3 })
	stop()
	if got := countLoggedPrefix("notify: telegram buttons") - before; got != 1 {
		t.Errorf("warnings = %d, want exactly 1 for repeated 409", got)
	}
}

func TestTelegramButtonsErrorsHideToken(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	orig, origMin, origMax := telegramGetUpdatesURL, telegramBackoffMin, telegramBackoffMax
	telegramGetUpdatesURL = url + "/bot%s/getUpdates"
	telegramBackoffMin, telegramBackoffMax = time.Millisecond, 2*time.Millisecond
	t.Cleanup(func() { telegramGetUpdatesURL, telegramBackoffMin, telegramBackoffMax = orig, origMin, origMax })

	m, _ := newMemMutes()
	before := countLoggedPrefix("notify: telegram buttons")
	stop := startButtons(t, tgBotNotifier("42", m))
	waitUntil(t, "a warning", func() bool { return countLoggedPrefix("notify: telegram buttons") > before })
	stop()
	for _, e := range logger.Snapshot(0) {
		if strings.Contains(e.Message, botToken) {
			t.Fatalf("token leaked into log: %q", e.Message)
		}
	}
}

func TestRunTelegramButtonsReturnsWhenNotConfigured(t *testing.T) {
	bot := newTgBot(t)
	m, _ := newMemMutes()
	cases := map[string]*Notifier{
		"disabled":          New(Config{TelegramToken: botToken, TelegramChatID: "42"}, "").WithTelegramMutes(m),
		"telegram disabled": New(Config{TelegramToken: botToken, TelegramChatID: "42", Enabled: true, TelegramDisabled: true}, "").WithTelegramMutes(m),
		"no token":          New(Config{TelegramChatID: "42", Enabled: true}, "").WithTelegramMutes(m),
		"no chat":           New(Config{TelegramToken: botToken, Enabled: true}, "").WithTelegramMutes(m),
		"no mutes":          New(Config{TelegramToken: botToken, TelegramChatID: "42", Enabled: true}, ""),
	}
	for name, n := range cases {
		t.Run(name, func(t *testing.T) {
			done := make(chan struct{})
			go func() {
				n.RunTelegramButtons(context.Background())
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("RunTelegramButtons did not return at once")
			}
		})
	}
	if p, _, _ := bot.snapshot(); p != 0 {
		t.Errorf("polls = %d, want 0", p)
	}
}

func TestTelegramButtonUnknownVerbKeepsMute(t *testing.T) {
	for _, data := range []string{"other:123", "mute:"} {
		t.Run(data, func(t *testing.T) {
			ctx := context.Background()
			bot := newTgBot(t, callbackBatch(100, data, `{"id":42}`))
			m, _ := newMemMutes()
			if err := m.Set(ctx, "123", true); err != nil {
				t.Fatal(err)
			}
			startButtons(t, tgBotNotifier("42", m))
			waitUntil(t, "second poll", func() bool { p, _, _ := bot.snapshot(); return p >= 2 })
			_, a, e := bot.snapshot()
			if a != 1 || e != 0 {
				t.Errorf("answers %d edits %d, want 1 and 0", a, e)
			}
			bot.mu.Lock()
			_, hasText := bot.answers[0]["text"]
			bot.mu.Unlock()
			if hasText {
				t.Error("bad data got a toast")
			}
			if muted, _ := m.Muted(ctx, "123"); !muted {
				t.Error("bad data unmuted the release")
			}
			if muted, _ := m.Muted(ctx, ""); muted {
				t.Error("bad data muted the empty hash")
			}
		})
	}
}

func muteButtonFor(n *Notifier) []tgButton {
	return n.tgMuteButtons(UpdatedEvent("N", "https://t/1", "").WithRelease("123", "rutracker", ""))
}

func TestTelegramMuteButtonHiddenWhileConflict(t *testing.T) {
	bot := newTgBot(t)
	bot.status = http.StatusConflict
	m, _ := newMemMutes()
	n := tgBotNotifier("42", m)
	if muteButtonFor(n) == nil {
		t.Fatal("button hidden before the first poll")
	}
	startButtons(t, n)
	waitUntil(t, "second poll", func() bool { p, _, _ := bot.snapshot(); return p >= 2 })
	if kb := muteButtonFor(n); kb != nil {
		t.Errorf("button %v shown while getUpdates answers 409", kb)
	}
}

func TestTelegramMuteButtonBackAfterSuccessfulPoll(t *testing.T) {
	bot := newTgBot(t, "", `{"ok":true,"result":[]}`)
	bot.statuses = []int{http.StatusConflict}
	m, _ := newMemMutes()
	n := tgBotNotifier("42", m)
	startButtons(t, n)
	waitUntil(t, "third poll", func() bool { p, _, _ := bot.snapshot(); return p >= 3 })
	kb := muteButtonFor(n)
	if len(kb) != 1 || kb[0].CallbackData != "mute:123" {
		t.Errorf("button %v after a successful poll, want mute:123", kb)
	}
}

func TestTelegramMuteButtonKeptOnOtherErrors(t *testing.T) {
	bot := newTgBot(t)
	bot.status = http.StatusInternalServerError
	m, _ := newMemMutes()
	n := tgBotNotifier("42", m)
	startButtons(t, n)
	waitUntil(t, "second poll", func() bool { p, _, _ := bot.snapshot(); return p >= 2 })
	if muteButtonFor(n) == nil {
		t.Error("button hidden by a non-409 error")
	}
}
