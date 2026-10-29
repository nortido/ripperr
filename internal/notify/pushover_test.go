package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func withPushover(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	orig := pushoverAPIURL
	pushoverAPIURL = srv.URL
	t.Cleanup(func() { pushoverAPIURL = orig })
}

func TestSendPushoverForm(t *testing.T) {
	var form url.Values
	var ct string
	withPushover(t, func(w http.ResponseWriter, r *http.Request) {
		ct = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		_, _ = io.WriteString(w, `{"status":1}`)
	})
	n := New(Config{PushoverToken: "apptok", PushoverUser: "userkey"}, "")
	long := strings.Repeat("ж", 2000)
	ev := Event{Kind: KindUpdated, Text: "Релиз:", Subject: long, URL: "https://example.com/t1"}
	if err := n.sendPushover(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if ct != "application/x-www-form-urlencoded" {
		t.Errorf("content-type = %q", ct)
	}
	if form.Get("token") != "apptok" || form.Get("user") != "userkey" {
		t.Errorf("form = %v", form)
	}
	if got := len([]rune(form.Get("message"))); got != 1024 {
		t.Errorf("message runes = %d, want 1024", got)
	}
	if form.Get("title") != "Релиз" {
		t.Errorf("title = %q", form.Get("title"))
	}
	if form.Get("url") != "https://example.com/t1" {
		t.Errorf("url = %q", form.Get("url"))
	}

	ev.URL = "https://example.com/" + strings.Repeat("a", 600)
	if err := n.sendPushover(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	if _, ok := form["url"]; ok {
		t.Errorf("url over 512 chars must be omitted: %q", form.Get("url"))
	}
}

func TestPushover429NotRetried(t *testing.T) {
	defer withRetryDelay(0)()
	var hits atomic.Int32
	withPushover(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"status":0,"errors":["quota"]}`)
	})
	n := New(Config{PushoverToken: "t", PushoverUser: "u"}, "")
	err := sendWithRetry(context.Background(), n.sendPushover, UpdatedEvent("x", "", ""), "pushover")
	if err == nil || hits.Load() != 1 {
		t.Fatalf("err = %v hits = %d, want error after exactly one attempt", err, hits.Load())
	}
}

func TestNotifyRetriesPushoverOn5xx(t *testing.T) {
	defer withRetryDelay(0)()
	var hits atomic.Int32
	withPushover(t, func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `{"status":1}`)
	})
	n := New(Config{PushoverToken: "t", PushoverUser: "u", Enabled: true}, "")
	n.Notify(TrackerDownEvent("rutracker", "boom"))
	waitHits(t, &hits, 2)
}

func TestNotifyRoutesPushoverByEvents(t *testing.T) {
	var hits atomic.Int32
	withPushover(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `{"status":1}`)
	})
	cfg := Config{PushoverToken: "t", PushoverUser: "u", Enabled: true, Events: []string{KindUpdated}}
	New(cfg, "").Notify(TrackerDownEvent("rutracker", "boom"))
	New(cfg, "").Notify(UpdatedEvent("x", "", ""))
	waitHits(t, &hits, 1)
	if got := hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1", got)
	}
}

func TestPushoverReady(t *testing.T) {
	if New(Config{PushoverToken: "t"}, "").HasTransport() || New(Config{PushoverUser: "u"}, "").HasTransport() {
		t.Error("pushover needs both token and user")
	}
	if !New(Config{PushoverToken: "t", PushoverUser: "u"}, "").HasTransport() {
		t.Error("token+user is a transport")
	}
}

func TestPushoverDigestIsShort(t *testing.T) {
	var form url.Values
	withPushover(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ = url.ParseQuery(string(b))
		_, _ = io.WriteString(w, `{"status":1}`)
	})
	n := New(Config{PushoverToken: "t", PushoverUser: "u", Language: "ru"}, "")
	if err := n.Send(context.Background(), MirrorDigestEvent(digestItems())); err != nil {
		t.Fatal(err)
	}
	if form.Get("title") != "Зеркала трекеров ждут решения" || form.Get("message") != "kinozal, rutor" {
		t.Errorf("title %q message %q", form.Get("title"), form.Get("message"))
	}
	if err := n.Send(context.Background(), TestEvent("")); err != nil {
		t.Fatal(err)
	}
	if form.Get("message") == "" {
		t.Error("pushover needs a non-empty message")
	}
}
