package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strings"
	"testing"
	"time"
)

func digestItems() []MirrorDigestItem {
	return []MirrorDigestItem{
		{Tracker: "kinozal", Kind: DigestMain, Host: "kinozal.me", From: "kinozal.tv"},
		{Tracker: "rutor", Kind: DigestRemove, Host: "rutor.a", Reason: DigestReasonDead},
		{Tracker: "rutor", Kind: DigestRemove, Host: "rutor.b", Reason: DigestReasonJackett},
		{Tracker: "rutor", Kind: DigestRemove, Host: "rutor.c", Reason: "redirect"},
		{Tracker: "kinozal", Kind: DigestConfirm, Host: "kinozal.guru"},
	}
}

func TestEventTextsByLanguage(t *testing.T) {
	cases := []struct {
		name   string
		ev     Event
		ru, en string
	}{
		{"updated", UpdatedEvent("Rel", "https://t/1", ""),
			"Раздача обновлена в torrent-клиенте:", "Release updated in the torrent client:"},
		{"broken", BrokenEvent("Rel", "u", 5),
			"Ошибок подряд: 5. Раздача отправлена в игнор и больше не проверяется:",
			"Errors in a row: 5. The release was moved to ignored and is no longer checked:"},
		{"tracker down", TrackerDownEvent("rutracker", "connection refused"),
			"Трекер rutracker недоступен: connection refused\nПроверки этого трекера приостановлены.",
			"Tracker rutracker is unavailable: connection refused\nChecks of this tracker are paused."},
		{"tracker up", TrackerUpEvent("rutracker"),
			"Трекер rutracker снова доступен, проверки возобновлены.",
			"Tracker rutracker is available again, checks resumed."},
		{"mirror switched", MirrorSwitchedEvent("kinozal", "kinozal.tv", "kinozal.me"),
			"Трекер kinozal: kinozal.tv не отвечает, проверки переключены на kinozal.me.",
			"Tracker kinozal: kinozal.tv is not responding, checks switched to kinozal.me."},
		{"mirror paused", MirrorPausedEvent("kinozal", "kinozal.xyz"),
			"Трекер kinozal на паузе: отвечает только новое зеркало kinozal.xyz. Подтвердите его в настройках трекера.",
			"Tracker kinozal is paused: only the new mirror kinozal.xyz responds. Confirm it in the tracker settings."},
		{"check failed", CheckFailedEvent("Rel", "u", "boom 100%"),
			"Ошибка проверки раздачи: boom 100%", "Release check failed: boom 100%"},
		{"test", TestEvent(""), "Ripperr: проверка связи", "Ripperr: connectivity test"},
		{"digest", MirrorDigestEvent(digestItems()),
			"Зеркала трекеров ждут решения:\nkinozal: основной адрес kinozal.me вместо kinozal.tv\nrutor: убрать rutor.a (не отвечает)\n" +
				"rutor: убрать rutor.b (нет в списке Jackett)\nrutor: убрать rutor.c\nkinozal: новое зеркало kinozal.guru ждет подтверждения",
			"Tracker mirrors need a decision:\nkinozal: main address kinozal.me instead of kinozal.tv\nrutor: remove rutor.a (not responding)\n" +
				"rutor: remove rutor.b (not in the Jackett list)\nrutor: remove rutor.c\nkinozal: new mirror kinozal.guru awaits confirmation"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			render := func(lang string) string {
				return New(Config{Language: lang}, "").localize(c.ev).Text
			}
			if got := render("ru"); got != c.ru {
				t.Errorf("ru = %q, want %q", got, c.ru)
			}
			for _, lang := range []string{"en", "", "de", "RU"} {
				if got := render(lang); got != c.en {
					t.Errorf("lang %q = %q, want %q", lang, got, c.en)
				}
			}
			if c.ev.Text != c.en {
				t.Errorf("builder Text = %q, want %q", c.ev.Text, c.en)
			}
		})
	}
}

func TestRawEventTextKept(t *testing.T) {
	ev := Event{Kind: KindUpdated, Text: "raw"}
	if got := New(Config{Language: "ru"}, "").localize(ev).Text; got != "raw" {
		t.Errorf("text = %q", got)
	}
}

func TestTestEventCarriesAppLink(t *testing.T) {
	ev := TestEvent("https://x/#dashboard")
	if ev.Kind != KindTest || ev.AppLink != "https://x/#dashboard" {
		t.Fatalf("ev = %+v", ev)
	}
}

func TestCatalogComplete(t *testing.T) {
	en, ru := messages[LangEN], messages[LangRU]
	if len(en) != len(ru) || len(en) == 0 {
		t.Fatalf("key counts en %d ru %d", len(en), len(ru))
	}
	for k, e := range en {
		r, ok := ru[k]
		if !ok {
			t.Errorf("key %q missing in ru", k)
			continue
		}
		if e == "" || r == "" {
			t.Errorf("key %q has an empty value", k)
		}
		if strings.Count(e, "%s") != strings.Count(r, "%s") {
			t.Errorf("key %q: %%s count differs: %q vs %q", k, e, r)
		}
		for _, v := range []string{e, r} {
			if strings.IndexFunc(v, func(r rune) bool { return r == 0x451 || r == 0x401 || r == 0x2014 }) >= 0 {
				t.Errorf("key %q: forbidden character in %q", k, v)
			}
		}
	}
}

func TestNormalizeLanguage(t *testing.T) {
	for in, want := range map[string]string{"ru": LangRU, "en": LangEN, "": LangEN, "RU": LangEN, "ru ": LangEN, "de": LangEN} {
		if got := NormalizeLanguage(in); got != want {
			t.Errorf("NormalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinkLabelByLanguage(t *testing.T) {
	link := testAppURL + "/#dashboard?name=x"
	base := Event{Kind: KindUpdated, Text: "T", Subject: "S", URL: "https://t/1", AppLink: link}
	for lang, label := range map[string]string{"": "Open in Ripperr", "en": "Open in Ripperr", "ru": "Открыть в Ripperr"} {
		n := New(Config{Language: lang}, "")
		ev := n.localize(base)

		m := buildTelegram(ev, maxMessage)
		if last := m.Links[len(m.Links)-1]; last.Text != "Ripperr" || last.URL != link {
			t.Errorf("lang %q telegram links = %+v", lang, m.Links)
		}

		raw := buildEmail(&mail.Address{Address: "bot@example.com"}, []*mail.Address{{Address: "a@example.com"}}, ev)
		msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(quotedprintable.NewReader(msg.Body))
		if !strings.HasSuffix(string(b), "\n"+label+": "+link) {
			t.Errorf("lang %q email body = %q", lang, b)
		}
	}
}

func TestPushoverLinkLabelByLanguage(t *testing.T) {
	var form url.Values
	withPushover(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		_, _ = io.WriteString(w, `{"status":1}`)
	})
	for lang, label := range map[string]string{"": "Open in Ripperr", "ru": "Открыть в Ripperr"} {
		n := New(Config{PushoverToken: "t", PushoverUser: "u", Language: lang}, "").WithAppURL(testAppURL)
		if err := n.Send(context.Background(), TrackerDownEvent("rutor", "boom")); err != nil {
			t.Fatal(err)
		}
		if form.Get("url_title") != label {
			t.Errorf("lang %q url_title = %q", lang, form.Get("url_title"))
		}
	}
}

func TestNotifyWebhookTextFollowsLanguage(t *testing.T) {
	for lang, want := range map[string]string{"": "Release updated in the torrent client:", "ru": "Раздача обновлена в torrent-клиенте:"} {
		got := make(chan string, 1)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var p map[string]any
			_ = json.NewDecoder(r.Body).Decode(&p)
			s, _ := p["text"].(string)
			got <- s
		}))
		New(Config{Enabled: true, WebhookURL: srv.URL, Language: lang}, "").Notify(UpdatedEvent("Rel", "https://t/1", ""))
		select {
		case s := <-got:
			if !strings.HasPrefix(s, want) {
				t.Errorf("lang %q text = %q, want prefix %q", lang, s, want)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("lang %q: no webhook call", lang)
		}
		srv.Close()
	}
}

func TestCheckFailedReason(t *testing.T) {
	for _, lang := range []string{LangRU, LangEN} {
		if got := CheckFailedReason(fmt.Sprintf(messages[lang][msgCheckFailed], "boom\nx")); got != "boom\nx" {
			t.Errorf("lang %q reason = %q, want %q", lang, got, "boom\nx")
		}
	}
	if got := CheckFailedReason("free text"); got != "free text" {
		t.Errorf("unknown text = %q, want unchanged", got)
	}
}
