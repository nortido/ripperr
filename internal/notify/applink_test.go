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
