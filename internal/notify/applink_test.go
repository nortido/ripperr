package notify

import (
	"context"
	"encoding/json"
	"io"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strings"
	"testing"
)

const testAppURL = "https://rip.example.com/app"

func TestAppPathPerKind(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
		want string
	}{
		{"updated", UpdatedEvent("Фильм & Ко 2026", "https://t/1", ""), "#dashboard?name=%D0%A4%D0%B8%D0%BB%D1%8C%D0%BC+%26+%D0%9A%D0%BE+2026"},
		{"broken", BrokenEvent("a b", "u", 3), "#dashboard?filter=ignored&name=a+b"},
		{"check failed", CheckFailedEvent("a b", "u", "boom"), "#dashboard?name=a+b"},
		{"tracker down", TrackerDownEvent("rutor", "x"), "#trackers"},
		{"tracker up", TrackerUpEvent("rutor"), "#trackers"},
		{"mirror switched", MirrorSwitchedEvent("rutor", "a", "b"), "#trackers"},
		{"mirror digest", MirrorDigestEvent([]MirrorDigestItem{{Tracker: "x", Kind: DigestConfirm, Host: "h"}}), "#trackers"},
		{"mirror paused", MirrorPausedEvent("rutor", "h"), "#trackers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.ev.AppPath != tc.want {
				t.Fatalf("AppPath = %q, want %q", tc.ev.AppPath, tc.want)
			}
		})
	}
	// router.js читает name через URLSearchParams: плюс там означает пробел
	q, err := url.ParseQuery(strings.SplitN(cases[0].ev.AppPath, "?", 2)[1])
	if err != nil || q.Get("name") != "Фильм & Ко 2026" {
		t.Fatalf("name round trip = %q, %v", q.Get("name"), err)
	}
}

func TestLinkJoin(t *testing.T) {
	cases := []struct{ base, path, want string }{
		{"https://h", "#trackers", "https://h/#trackers"},
		{testAppURL, "#trackers", testAppURL + "/#trackers"},
		{"", "#trackers", ""},
		{"https://h", "", ""},
	}
	for _, tc := range cases {
		if got := Link(tc.base, tc.path); got != tc.want {
			t.Errorf("Link(%q,%q) = %q, want %q", tc.base, tc.path, got, tc.want)
		}
	}
}

func TestWebhookCarriesAppURL(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = nil
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	ev := TrackerDownEvent("rutor", "boom")

	n := New(Config{WebhookURL: srv.URL}, "").WithAppURL(testAppURL)
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if got["app_url"] != testAppURL+"/#trackers" {
		t.Errorf("app_url = %q", got["app_url"])
	}

	if err := New(Config{WebhookURL: srv.URL}, "").Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["app_url"]; ok {
		t.Errorf("app_url must be omitted when app URL is empty: %v", got)
	}
}

func TestSendUsesPresetAppLink(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
	}))
	defer srv.Close()
	ev := Event{Kind: KindTest, Text: "t", AppLink: "https://x/#dashboard"}
	if err := New(Config{WebhookURL: srv.URL}, "").Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if got["app_url"] != "https://x/#dashboard" {
		t.Errorf("app_url = %q", got["app_url"])
	}
}

func TestTelegramAppLinkLine(t *testing.T) {
	link := testAppURL + "/#dashboard?name=x"
	t.Run("html", func(t *testing.T) {
		ev := Event{Kind: KindUpdated, Text: "Обновлено:", Subject: "Релиз <1>", URL: "https://t/1", AppLink: link}
		text, mode := telegramText(ev, maxMessage)
		want := "Обновлено:\n" + `<a href="https://t/1">Релиз &lt;1&gt;</a>` + "\n" + `<a href="` + link + `">Open in Ripperr</a>`
		if mode != "HTML" || text != want {
			t.Fatalf("mode %q text %q, want %q", mode, text, want)
		}
	})
	t.Run("escapes href", func(t *testing.T) {
		ev := Event{Kind: KindUpdated, Text: "t", Subject: "s", URL: "https://t/1", AppLink: "https://h/#d?a=1&b=2"}
		text, _ := telegramText(ev, maxMessage)
		if !strings.Contains(text, `href="https://h/#d?a=1&amp;b=2"`) {
			t.Fatalf("text = %q", text)
		}
	})
	t.Run("plain", func(t *testing.T) {
		ev := Event{Kind: KindTrackerDown, Text: "Трекер лежит", AppLink: testAppURL + "/#trackers"}
		text, mode := telegramText(ev, maxMessage)
		if mode != "" || text != "Трекер лежит\nOpen in Ripperr: "+testAppURL+"/#trackers" {
			t.Fatalf("mode %q text %q", mode, text)
		}
	})
	t.Run("overlong text drops link", func(t *testing.T) {
		ev := Event{Kind: KindTrackerDown, Text: strings.Repeat("ж", 5000), AppLink: testAppURL + "/#trackers"}
		text, _ := telegramText(ev, maxMessage)
		if n := len([]rune(text)); n != maxMessage || strings.Contains(text, "Ripperr") {
			t.Fatalf("runes %d, want text cut to the limit without the link", n)
		}
	})
	t.Run("html limit keeps link", func(t *testing.T) {
		ev := Event{Kind: KindUpdated, Text: "t", Subject: strings.Repeat("ж", 2000), URL: "https://t/1", AppLink: link}
		text, _ := telegramText(ev, maxCaption)
		if n := len([]rune(stripTags(text))); n > maxCaption || !strings.Contains(text, `">Open in Ripperr</a>`) {
			t.Fatalf("visible runes %d", n)
		}
	})
	t.Run("no link", func(t *testing.T) {
		ev := Event{Kind: KindTrackerDown, Text: "Трекер лежит"}
		if text, _ := telegramText(ev, maxMessage); text != "Трекер лежит" {
			t.Fatalf("text = %q", text)
		}
	})
}

func TestTelegramSendCarriesAppLink(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}))
	defer srv.Close()
	orig := telegramAPIURL
	telegramAPIURL = srv.URL + "/bot%s/sendMessage"
	defer func() { telegramAPIURL = orig }()
	n := New(Config{TelegramToken: "tok", TelegramChatID: "1"}, "").WithAppURL(testAppURL)
	if err := n.Send(context.Background(), BrokenEvent("a b", "https://t/1", 3)); err != nil {
		t.Fatal(err)
	}
	text, _ := body["text"].(string)
	want := testAppURL + "/#dashboard?filter=ignored&amp;name=a+b"
	if !strings.Contains(text, want) || !strings.Contains(text, "Open in Ripperr") {
		t.Fatalf("text = %q, want link %q", text, want)
	}
}

func TestEmailBodyAppLink(t *testing.T) {
	from := &mail.Address{Address: "bot@example.com"}
	to := []*mail.Address{{Address: "a@example.com"}}
	body := func(ev Event) string {
		raw := buildEmail(from, to, ev)
		msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	link := testAppURL + "/#dashboard?name=x"
	if got := body(Event{Kind: KindUpdated, Text: "T", Subject: "S", URL: "https://t/1", AppLink: link}); !strings.HasSuffix(got, "\nOpen in Ripperr: "+link) {
		t.Errorf("body = %q", got)
	}
	if got := body(Event{Kind: KindUpdated, Text: "T", Subject: "S", URL: "https://t/1"}); strings.Contains(got, "Ripperr") {
		t.Errorf("no link expected: %q", got)
	}
}

func TestPushoverAppLink(t *testing.T) {
	var form url.Values
	withPushover(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		_, _ = io.WriteString(w, `{"status":1}`)
	})
	n := New(Config{PushoverToken: "t", PushoverUser: "u"}, "").WithAppURL(testAppURL)

	if err := n.Send(context.Background(), TrackerDownEvent("rutor", "boom")); err != nil {
		t.Fatal(err)
	}
	if form.Get("url") != testAppURL+"/#trackers" || form.Get("url_title") != "Open in Ripperr" {
		t.Errorf("url %q title %q", form.Get("url"), form.Get("url_title"))
	}

	// единственное поле url занято ссылкой на раздачу, ссылка приложения уходит в текст
	if err := n.Send(context.Background(), UpdatedEvent("x", "https://t/1", "")); err != nil {
		t.Fatal(err)
	}
	if form.Get("url") != "https://t/1" || !strings.Contains(form.Get("message"), testAppURL+"/#dashboard?name=x") {
		t.Errorf("url %q message %q", form.Get("url"), form.Get("message"))
	}

	long := Event{Kind: KindUpdated, Text: strings.Repeat("ж", 2000), Subject: "s", URL: "https://t/1", AppLink: testAppURL + "/#d"}
	if err := n.sendPushover(context.Background(), long); err != nil {
		t.Fatal(err)
	}
	if m := form.Get("message"); len([]rune(m)) != 1024 || strings.Contains(m, "Ripperr") {
		t.Errorf("message runes %d", len([]rune(m)))
	}

	if err := New(Config{PushoverToken: "t", PushoverUser: "u"}, "").Send(context.Background(), TrackerDownEvent("rutor", "boom")); err != nil {
		t.Fatal(err)
	}
	if _, ok := form["url"]; ok || strings.Contains(form.Get("message"), "Ripperr") {
		t.Errorf("no link expected: %v", form)
	}
}

func TestPushoverDropsAppLinkThatDoesNotFit(t *testing.T) {
	var form url.Values
	withPushover(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		_, _ = io.WriteString(w, `{"status":1}`)
	})
	n := New(Config{PushoverToken: "t", PushoverUser: "u"}, "").WithAppURL(testAppURL)
	// каждая Ж в ссылке занимает 6 знаков (%D0%96), сама ссылка длиннее лимита сообщения
	ev := UpdatedEvent(strings.Repeat("Ж", 190), "https://t/1", "")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	m := form.Get("message")
	if len([]rune(m)) > maxPushoverMessage || strings.Contains(m, "Ripperr") || form.Get("url") != "https://t/1" {
		t.Errorf("message runes %d, url %q, link must be dropped", len([]rune(m)), form.Get("url"))
	}
}

func TestTelegramPlainDropsAppLinkThatDoesNotFit(t *testing.T) {
	ev := Event{Kind: KindTrackerDown, Text: "t", AppLink: testAppURL + "/#dashboard?name=" + strings.Repeat("%D0%96", 190)}
	text, mode := telegramText(ev, maxCaption)
	if n := len([]rune(text)); n > maxCaption || mode != "" || strings.Contains(text, "Ripperr") || text != "t" {
		t.Fatalf("runes %d mode %q text %q, want the link dropped", n, mode, text)
	}
}
