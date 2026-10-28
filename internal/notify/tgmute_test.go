package notify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func newMemMutes() (*TelegramMutes, *memSettings) {
	db := &memSettings{rows: map[string]json.RawMessage{}}
	return NewTelegramMutes(db), db
}

func TestTelegramMutesRoundTrip(t *testing.T) {
	ctx := context.Background()
	m, db := newMemMutes()
	if got, err := m.Muted(ctx, "1"); err != nil || got {
		t.Fatalf("empty store: muted %v err %v", got, err)
	}
	for _, h := range []string{"b", "a", "a"} {
		if err := m.Set(ctx, h, true); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := m.Muted(ctx, "a"); !got {
		t.Error("a not muted after Set")
	}
	if got, _ := m.Muted(ctx, "c"); got {
		t.Error("c muted without Set")
	}
	if raw := string(db.rows["telegram_mutes"]); raw != `{"url_hashes":["a","b"]}` {
		t.Errorf("stored doc %s, want sorted unique", raw)
	}
	if err := m.Set(ctx, "a", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Muted(ctx, "a"); got {
		t.Error("a still muted after unmute")
	}
	if raw := string(db.rows["telegram_mutes"]); raw != `{"url_hashes":["b"]}` {
		t.Errorf("stored doc %s", raw)
	}
}

func TestTelegramMutesInvalidRowReadsEmptyAndIsRewritten(t *testing.T) {
	ctx := context.Background()
	m, db := newMemMutes()
	db.rows["telegram_mutes"] = json.RawMessage(`{broken`)
	if got, err := m.Muted(ctx, "1"); err != nil || got {
		t.Fatalf("invalid row: muted %v err %v", got, err)
	}
	if err := m.Set(ctx, "1", true); err != nil {
		t.Fatal(err)
	}
	if raw := string(db.rows["telegram_mutes"]); raw != `{"url_hashes":["1"]}` {
		t.Errorf("stored doc %s, want rewritten valid doc", raw)
	}
}

type failingSettings struct{ memSettings }

func (f *failingSettings) GetSetting(context.Context, string) (json.RawMessage, bool, error) {
	return nil, false, errors.New("db down")
}

func tgMuteNotifier(m *TelegramMutes) *Notifier {
	return New(Config{TelegramToken: "tok", TelegramChatID: "42", Language: "ru"}, "").WithAppURL(testAppURL).WithTelegramMutes(m)
}

func TestTelegramMutedUpdatedSkipsTelegramOnly(t *testing.T) {
	_, got := tgServer(t, func(int, tgReq) int { return http.StatusOK })
	hook, hits := countingServer(t)
	m, _ := newMemMutes()
	if err := m.Set(context.Background(), "7", true); err != nil {
		t.Fatal(err)
	}
	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", WebhookURL: hook.URL}, "").WithTelegramMutes(m)

	if err := n.Send(context.Background(), UpdatedEvent("A", "https://t/1", "").WithRelease("7", "rutracker", "")); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 0 {
		t.Errorf("muted release reached telegram: %+v", reqs)
	}
	if hits.Load() != 1 {
		t.Errorf("webhook hits = %d, want 1 (mute is telegram only)", hits.Load())
	}

	if err := n.Send(context.Background(), BrokenEvent("A", "https://t/1", 3).WithRelease("7", "rutracker", "")); err != nil {
		t.Fatal(err)
	}
	if err := n.Send(context.Background(), UpdatedEvent("B", "https://t/2", "").WithRelease("8", "rutracker", "")); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 2 {
		t.Errorf("telegram requests = %d, want 2 (other kind and other release)", len(reqs))
	}
}

func TestTelegramMuteReadErrorStillSends(t *testing.T) {
	_, got := tgServer(t, func(int, tgReq) int { return http.StatusOK })
	n := tgMuteNotifier(NewTelegramMutes(&failingSettings{}))
	if err := n.Send(context.Background(), UpdatedEvent("A", "https://t/1", "").WithRelease("7", "", "")); err != nil {
		t.Fatal(err)
	}
	if reqs := got(); len(reqs) != 1 {
		t.Errorf("requests = %d, want 1 (a lost notification is worse than a muted one)", len(reqs))
	}
}

func TestTelegramMuteButtonInRowOne(t *testing.T) {
	m, _ := newMemMutes()
	cases := []struct {
		name string
		n    *Notifier
		ev   Event
		want []string
	}{
		{"with mutes", tgMuteNotifier(m), UpdatedEvent("A", "https://t/1", "").WithRelease("123", "r", ""), []string{"Тема на трекере", "Открыть в Ripperr", "🔕"}},
		{"no mutes", New(Config{TelegramToken: "tok", TelegramChatID: "42", Language: "ru"}, "").WithAppURL(testAppURL), UpdatedEvent("A", "https://t/1", "").WithRelease("123", "r", ""), []string{"Тема на трекере", "Открыть в Ripperr"}},
		{"no hash", tgMuteNotifier(m), UpdatedEvent("A", "https://t/1", ""), []string{"Тема на трекере", "Открыть в Ripperr"}},
		{"other kind", tgMuteNotifier(m), BrokenEvent("A", "https://t/1", 3).WithRelease("123", "r", ""), []string{"Тема на трекере", "Открыть в Ripperr"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got := tgServer(t, func(int, tgReq) int { return http.StatusOK })
			if err := tc.n.Send(context.Background(), tc.ev); err != nil {
				t.Fatal(err)
			}
			rows := tgButtonsOf(t, got()[0].body["reply_markup"])
			if len(rows) != 1 {
				t.Fatalf("rows = %d, want 1: %+v", len(rows), rows)
			}
			var texts []string
			for _, b := range rows[0] {
				texts = append(texts, b["text"])
			}
			if strings.Join(texts, "|") != strings.Join(tc.want, "|") {
				t.Errorf("buttons %v, want %v", texts, tc.want)
			}
			if tc.want[len(tc.want)-1] == "🔕" {
				if b := rows[0][len(rows[0])-1]; b["callback_data"] != "mute:123" || b["url"] != "" {
					t.Errorf("mute button %+v", b)
				}
			}
		})
	}
}

func TestTelegramMuteButtonAloneWithoutLinks(t *testing.T) {
	m, _ := newMemMutes()
	_, got := tgServer(t, func(int, tgReq) int { return http.StatusOK })
	n := New(Config{TelegramToken: "tok", TelegramChatID: "42"}, "").WithTelegramMutes(m)
	if err := n.Send(context.Background(), UpdatedEvent("A", "", "").WithRelease("5", "", "")); err != nil {
		t.Fatal(err)
	}
	rows := tgButtonsOf(t, got()[0].body["reply_markup"])
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0]["callback_data"] != "mute:5" {
		t.Errorf("markup = %+v, want the lone mute button", rows)
	}
}

func TestTelegramFallbackKeepsMuteButton(t *testing.T) {
	m, _ := newMemMutes()
	_, got := tgServer(t, func(n int, _ tgReq) int {
		if n == 1 {
			return http.StatusBadRequest
		}
		return http.StatusOK
	})
	n := tgMuteNotifier(m)
	if err := n.Send(context.Background(), UpdatedEvent("A", "https://t/1", "").WithRelease("123", "r", "")); err != nil {
		t.Fatal(err)
	}
	reqs := got()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	rows := tgButtonsOf(t, reqs[1].body["reply_markup"])
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0]["text"] != "🔕" || rows[0][0]["callback_data"] != "mute:123" || rows[0][0]["url"] != "" {
		t.Errorf("fallback markup = %+v, want only the mute button", rows)
	}
	if text, _ := reqs[1].body["text"].(string); !strings.Contains(text, `<a href="https://t/1">Тема на трекере</a>`) {
		t.Errorf("fallback text lost links: %q", text)
	}
}
