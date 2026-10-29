package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"
)

type tgReq struct {
	path   string
	body   map[string]any
	fields map[string]string
}

func tgLocalized(lang string, ev Event) Event {
	return New(Config{Language: lang}, "").WithAppURL(testAppURL).withLink(New(Config{Language: lang}, "").localize(ev))
}

func tgServer(t *testing.T, status func(n int, r tgReq) int) (*httptest.Server, func() []tgReq) {
	t.Helper()
	var mu sync.Mutex
	var reqs []tgReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := tgReq{path: r.URL.Path}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			req.fields = map[string]string{}
			for k, v := range r.MultipartForm.Value {
				req.fields[k] = v[0]
			}
		} else {
			_ = json.NewDecoder(r.Body).Decode(&req.body)
		}
		mu.Lock()
		reqs = append(reqs, req)
		code := status(len(reqs), req)
		mu.Unlock()
		w.WriteHeader(code)
		if code >= 400 {
			_, _ = w.Write([]byte(`{"ok":false,"description":"Bad Request: wrong url"}`))
		}
	}))
	t.Cleanup(srv.Close)
	origMsg, origPhoto := telegramAPIURL, telegramPhotoURL
	telegramAPIURL = srv.URL + "/bot%s/sendMessage"
	telegramPhotoURL = srv.URL + "/bot%s/sendPhoto"
	t.Cleanup(func() { telegramAPIURL, telegramPhotoURL = origMsg, origPhoto })
	return srv, func() []tgReq {
		mu.Lock()
		defer mu.Unlock()
		return append([]tgReq(nil), reqs...)
	}
}

func tgButtonsOf(t *testing.T, raw any) [][]map[string]string {
	t.Helper()
	markup, _ := raw.(map[string]any)
	rows, _ := markup["inline_keyboard"].([]any)
	var out [][]map[string]string
	for _, r := range rows {
		var row []map[string]string
		for _, b := range r.([]any) {
			m := map[string]string{}
			for k, v := range b.(map[string]any) {
				m[k], _ = v.(string)
			}
			row = append(row, m)
		}
		out = append(out, row)
	}
	return out
}

func TestTelegramLayoutPerKind(t *testing.T) {
	updated := UpdatedEvent("Some.Release.2026", "https://t/1", "").WithRelease("1", "rutracker", "sonarr")
	appUpdated := testAppURL + "/" + updated.AppPath
	cases := []struct {
		name  string
		lang  string
		ev    Event
		text  string
		links []tgButton
	}{
		{"updated ru", "ru", updated,
			"<b>🔄 Раздача обновлена</b>\nSome Release 2026\n<i>rutracker · sonarr</i>",
			[]tgButton{{Text: "Тема на трекере", URL: "https://t/1"}, {Text: "Открыть в Ripperr", URL: appUpdated}}},
		{"updated en", "en", updated,
			"<b>🔄 Release updated</b>\nSome Release 2026\n<i>rutracker · sonarr</i>",
			[]tgButton{{Text: "Topic on tracker", URL: "https://t/1"}, {Text: "Open in Ripperr", URL: appUpdated}}},
		{"updated without tracker", "ru", UpdatedEvent("Name", "", "").WithRelease("1", "", "sonarr"),
			"<b>🔄 Раздача обновлена</b>\nName\n<i>sonarr</i>",
			[]tgButton{{Text: "Открыть в Ripperr", URL: testAppURL + "/#dashboard?name=Name"}}},
		{"updated without release info", "ru", UpdatedEvent("Name", "https://t/1", ""),
			"<b>🔄 Раздача обновлена</b>\nName",
			[]tgButton{{Text: "Тема на трекере", URL: "https://t/1"}, {Text: "Открыть в Ripperr", URL: testAppURL + "/#dashboard?name=Name"}}},
		{"broken ru", "ru", BrokenEvent("Name.2026", "https://t/2", 3),
			"<b>⚠️ Раздача ушла в игнор</b>\nName 2026\n<i>Ошибок подряд: 3, больше не проверяется</i>",
			[]tgButton{{Text: "Тема на трекере", URL: "https://t/2"}, {Text: "Открыть в Ripperr", URL: testAppURL + "/#dashboard?filter=ignored&name=Name.2026"}}},
		{"broken en", "en", BrokenEvent("Name", "", 3),
			"<b>⚠️ Release moved to ignored</b>\nName\n<i>Errors in a row: 3, no longer checked</i>",
			[]tgButton{{Text: "Open in Ripperr", URL: testAppURL + "/#dashboard?filter=ignored&name=Name"}}},
		{"tracker down ru", "ru", TrackerDownEvent("kinozal", "timeout"),
			"<b>🔴 Трекер недоступен</b>\nkinozal: timeout\n<i>Проверки трекера на паузе</i>",
			[]tgButton{{Text: "Открыть в Ripperr", URL: testAppURL + "/#trackers"}}},
		{"tracker down en", "en", TrackerDownEvent("kinozal", "timeout"),
			"<b>🔴 Tracker unavailable</b>\nkinozal: timeout\n<i>Tracker checks paused</i>",
			[]tgButton{{Text: "Open in Ripperr", URL: testAppURL + "/#trackers"}}},
		{"tracker up ru", "ru", TrackerUpEvent("kinozal"),
			"<b>🟢 Трекер снова доступен</b>\nkinozal\n<i>Проверки возобновлены</i>",
			[]tgButton{{Text: "Открыть в Ripperr", URL: testAppURL + "/#trackers"}}},
		{"tracker up en", "en", TrackerUpEvent("kinozal"),
			"<b>🟢 Tracker is back</b>\nkinozal\n<i>Checks resumed</i>",
			[]tgButton{{Text: "Open in Ripperr", URL: testAppURL + "/#trackers"}}},
		{"check failed ru", "ru", CheckFailedEvent("Name", "https://t/3", "boom"),
			"<b>❌ Ошибка проверки</b>\nName\n<i>boom</i>",
			[]tgButton{{Text: "Тема на трекере", URL: "https://t/3"}, {Text: "Открыть в Ripperr", URL: testAppURL + "/#dashboard?name=Name"}}},
		{"check failed en", "en", CheckFailedEvent("Name", "https://t/3", "boom"),
			"<b>❌ Check failed</b>\nName\n<i>boom</i>",
			[]tgButton{{Text: "Topic on tracker", URL: "https://t/3"}, {Text: "Open in Ripperr", URL: testAppURL + "/#dashboard?name=Name"}}},
		{"test ru", "ru", TestEvent("https://x/#dashboard"),
			"<b>✅ Ripperr: проверка связи</b>",
			[]tgButton{{Text: "Открыть в Ripperr", URL: "https://x/#dashboard"}}},
		{"mirror ru", "ru", MirrorSwitchedEvent("rutor", "a.example", "b.example"),
			"<b>🔀 Зеркало переключено</b>\nrutor: b.example",
			[]tgButton{{Text: "Открыть в Ripperr", URL: testAppURL + "/#trackers"}}},
		{"mirror paused ru", "ru", MirrorPausedEvent("rutor", "c.example"),
			"<b>🔀 Трекер на паузе</b>\nrutor\n<i>Подтвердите зеркало c.example</i>",
			[]tgButton{{Text: "Открыть в Ripperr", URL: testAppURL + "/#trackers"}}},
		{"digest ru", "ru", MirrorDigestEvent(digestItems()),
			"<b>🔀 Зеркала трекеров ждут решения</b>\nkinozal, rutor",
			[]tgButton{{Text: "Открыть в Ripperr", URL: testAppURL + "/#trackers"}}},
		{"long error is cut", "en", CheckFailedEvent("Name", "", strings.Repeat("e", 300)),
			"<b>❌ Check failed</b>\nName\n<i>" + strings.Repeat("e", maxReason-1) + "…</i>",
			[]tgButton{{Text: "Open in Ripperr", URL: testAppURL + "/#dashboard?name=Name"}}},
		{"multi-line body", "ru", Event{Kind: KindMirrorSwitched, Text: "Head <1>\nrow & two\nrow three"},
			"<b>🔀 Head &lt;1&gt;</b>\nrow &amp; two\nrow three", nil},
		{"unknown kind has no emoji", "ru", Event{Kind: "custom", Text: "Just text"}, "<b>Just text</b>", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := buildTelegram(tgLocalized(tc.lang, tc.ev), maxMessage)
			if m.Text != tc.text {
				t.Errorf("text:\n got %q\nwant %q", m.Text, tc.text)
			}
			if len(m.Links) != len(tc.links) {
				t.Fatalf("links = %+v, want %+v", m.Links, tc.links)
			}
			for i := range tc.links {
				if m.Links[i] != tc.links[i] {
					t.Errorf("link %d = %+v, want %+v", i, m.Links[i], tc.links[i])
				}
			}
			if strings.Contains(m.Text, "http") {
				t.Errorf("text %q carries a raw url", m.Text)
			}
		})
	}
}

func TestTelegramLayoutEscapesHTML(t *testing.T) {
	ev := UpdatedEvent("A <b> & c", "https://t/1", "").WithRelease("1", "t&r", ">x")
	m := buildTelegram(tgLocalized("ru", ev), maxMessage)
	want := "<b>🔄 Раздача обновлена</b>\nA &lt;b&gt; &amp; c\n<i>t&amp;r · &gt;x</i>"
	if m.Text != want {
		t.Fatalf("text %q, want %q", m.Text, want)
	}
}

func TestTelegramLayoutLimits(t *testing.T) {
	long := strings.Repeat("ж", 2000)
	cases := []struct {
		name  string
		ev    Event
		limit int
	}{
		{"long name caption", UpdatedEvent(long, "https://t/1", "").WithRelease("1", "rutracker", "sonarr"), maxCaption},
		{"long name and context", UpdatedEvent(long, "https://t/1", "").WithRelease("1", strings.Repeat("т", 3000), strings.Repeat("к", 3000)), maxCaption},
		{"long error", CheckFailedEvent("Name", "https://t/1", strings.Repeat("e", 6000)), maxMessage},
		{"long generic", Event{Kind: KindMirrorSwitched, Text: strings.Repeat("ж", 5000), AppLink: testAppURL + "/#trackers"}, maxMessage},
		{"long generic caption", Event{Kind: KindMirrorSwitched, Text: "t\n" + strings.Repeat("ж", 5000)}, maxCaption},
		{"emoji name caption", UpdatedEvent(strings.Repeat("😀", 2000), "https://t/1", "").WithRelease("1", "rutracker", "sonarr"), maxCaption},
		{"emoji name message", UpdatedEvent(strings.Repeat("😀", 5000), "https://t/1", "").WithRelease("1", "rutracker", "sonarr"), maxMessage},
		{"emoji context", UpdatedEvent("Name", "https://t/1", "").WithRelease("1", strings.Repeat("🔴", 2000), strings.Repeat("😀", 2000)), maxCaption},
		{"emoji error", CheckFailedEvent("Name", "https://t/1", strings.Repeat("😀", 5000)), maxMessage},
		{"emoji generic", Event{Kind: KindMirrorSwitched, Text: "t\n" + strings.Repeat("😀", 5000)}, maxMessage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := tgLocalized("ru", tc.ev)
			m := buildTelegram(ev, tc.limit)
			for name, text := range map[string]string{"text": m.Text, "with links": m.withLinks()} {
				if n := len(utf16.Encode([]rune(stripTags(text)))); n > tc.limit {
					t.Errorf("%s: visible %d UTF-16 units, limit %d", name, n, tc.limit)
				}
			}
			if !strings.HasPrefix(m.Text, "<b>") || strings.Count(m.Text, "<b>") != 1 || strings.Count(m.Text, "</b>") != 1 {
				t.Errorf("header markup damaged: %.60q", m.Text)
			}
			if strings.Contains(m.Text, "<i>") && !strings.HasSuffix(m.Text, "</i>") {
				t.Errorf("italic line cut: %.60q", m.Text[len(m.Text)-30:])
			}
			if len(m.Links) != len(tgLinksFor(ev)) {
				t.Errorf("links lost: %+v", m.Links)
			}
		})
	}
}

func tgLinksFor(ev Event) []string {
	var out []string
	if ev.URL != "" {
		out = append(out, ev.URL)
	}
	if ev.AppLink != "" {
		out = append(out, ev.AppLink)
	}
	return out
}

func TestTelegramSendMessageCarriesLayoutAndButtons(t *testing.T) {
	_, got := tgServer(t, func(int, tgReq) int { return http.StatusOK })
	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Language: "ru"}, "").WithAppURL(testAppURL)
	ev := UpdatedEvent("Some.Release.2026", "https://t/1", "").WithRelease("1", "rutracker", "sonarr")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	reqs := got()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	b := reqs[0].body
	if b["parse_mode"] != "HTML" || b["disable_web_page_preview"] != true {
		t.Errorf("parse_mode %v preview %v", b["parse_mode"], b["disable_web_page_preview"])
	}
	if want := "<b>🔄 Раздача обновлена</b>\nSome Release 2026\n<i>rutracker · sonarr</i>"; b["text"] != want {
		t.Errorf("text %q, want %q", b["text"], want)
	}
	rows := tgButtonsOf(t, b["reply_markup"])
	if len(rows) != 1 || len(rows[0]) != 2 ||
		rows[0][0]["text"] != "Тема на трекере" || rows[0][0]["url"] != "https://t/1" ||
		rows[0][1]["text"] != "Открыть в Ripperr" || rows[0][1]["url"] != testAppURL+"/"+ev.AppPath {
		t.Errorf("inline_keyboard = %+v", rows)
	}
}

func TestTelegramSendWithoutLinksHasNoMarkup(t *testing.T) {
	_, got := tgServer(t, func(int, tgReq) int { return http.StatusOK })
	n := New(Config{TelegramToken: "tok", TelegramChatID: "42"}, "")
	if err := n.Send(context.Background(), Event{Kind: "custom", Text: "ping"}); err != nil {
		t.Fatal(err)
	}
	reqs := got()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d", len(reqs))
	}
	if _, ok := reqs[0].body["reply_markup"]; ok {
		t.Errorf("reply_markup = %v, want none without links", reqs[0].body["reply_markup"])
	}
	if reqs[0].body["text"] != "<b>ping</b>" || reqs[0].body["parse_mode"] != "HTML" {
		t.Errorf("body = %v", reqs[0].body)
	}
}

func TestTelegramBadRequestRetriesWithoutURLButtons(t *testing.T) {
	_, got := tgServer(t, func(n int, _ tgReq) int {
		if n == 1 {
			return http.StatusBadRequest
		}
		return http.StatusOK
	})
	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Language: "ru"}, "").WithAppURL(testAppURL)
	ev := UpdatedEvent("Name", "https://t/1?a=1&b=2", "").WithRelease("1", "rutracker", "sonarr")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	reqs := got()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	if _, ok := reqs[1].body["reply_markup"]; ok {
		t.Errorf("retry still carries reply_markup %v", reqs[1].body["reply_markup"])
	}
	text, _ := reqs[1].body["text"].(string)
	for _, want := range []string{
		`<a href="https://t/1?a=1&amp;b=2">Тема на трекере</a>`,
		`<a href="` + testAppURL + `/#dashboard?name=Name">Открыть в Ripperr</a>`,
		"<b>🔄 Раздача обновлена</b>\nName\n<i>rutracker · sonarr</i>\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("retry text %q lacks %q", text, want)
		}
	}
}

func TestTelegramBadRequestTwiceReturnsError(t *testing.T) {
	_, got := tgServer(t, func(int, tgReq) int { return http.StatusBadRequest })
	n := New(Config{TelegramToken: "tok", TelegramChatID: "42"}, "").WithAppURL(testAppURL)
	err := n.Send(context.Background(), UpdatedEvent("Name", "https://t/1", ""))
	if err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("err = %v, want status 400", err)
	}
	if len(got()) != 2 {
		t.Errorf("requests = %d, want exactly 2", len(got()))
	}
}

func TestTelegramFallbackOnlyFor400WithURLButtons(t *testing.T) {
	t.Run("403 not retried", func(t *testing.T) {
		_, got := tgServer(t, func(int, tgReq) int { return http.StatusForbidden })
		n := New(Config{TelegramToken: "tok", TelegramChatID: "42"}, "")
		if err := n.Send(context.Background(), UpdatedEvent("Name", "https://t/1", "")); err == nil {
			t.Fatal("want error")
		}
		if len(got()) != 1 {
			t.Errorf("requests = %d, want 1", len(got()))
		}
	})
	t.Run("400 without buttons not retried", func(t *testing.T) {
		_, got := tgServer(t, func(int, tgReq) int { return http.StatusBadRequest })
		n := New(Config{TelegramToken: "tok", TelegramChatID: "42"}, "")
		if err := n.Send(context.Background(), Event{Kind: "custom", Text: "ping"}); err == nil {
			t.Fatal("want error")
		}
		if len(got()) != 1 {
			t.Errorf("requests = %d, want 1", len(got()))
		}
	})
}

func TestTelegramPhotoUploadCarriesMarkupAndKeepsMethodOnFallback(t *testing.T) {
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("JPEGBYTES"))
	}))
	defer img.Close()
	_, got := tgServer(t, func(n int, _ tgReq) int {
		if n == 1 {
			return http.StatusBadRequest
		}
		return http.StatusOK
	})
	origMsg := telegramAPIURL
	defer func() { telegramAPIURL = origMsg }()
	telegramAPIURL = "http://127.0.0.1:1/bot%s/sendMessage"

	n := New(Config{TelegramToken: "tok", TelegramChatID: "42", Language: "ru"}, "").WithAppURL(testAppURL)
	n.posterHC = img.Client()
	ev := UpdatedEvent("Name", "https://t/1", img.URL+"/p.jpg").WithRelease("1", "rutracker", "sonarr")
	if err := n.Send(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	reqs := got()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	for i, r := range reqs {
		if !strings.HasSuffix(r.path, "/sendPhoto") || r.fields["parse_mode"] != "HTML" {
			t.Errorf("request %d: path %q parse_mode %q", i, r.path, r.fields["parse_mode"])
		}
	}
	var markup map[string]any
	if err := json.Unmarshal([]byte(reqs[0].fields["reply_markup"]), &markup); err != nil {
		t.Fatalf("reply_markup field %q: %v", reqs[0].fields["reply_markup"], err)
	}
	rows := tgButtonsOf(t, markup)
	if len(rows) != 1 || len(rows[0]) != 2 || rows[0][0]["url"] != "https://t/1" || rows[0][1]["url"] != testAppURL+"/"+ev.AppPath {
		t.Errorf("inline_keyboard = %+v", rows)
	}
	if _, ok := reqs[1].fields["reply_markup"]; ok {
		t.Errorf("retry carries reply_markup %q", reqs[1].fields["reply_markup"])
	}
	if c := reqs[1].fields["caption"]; !strings.Contains(c, `<a href="https://t/1">Тема на трекере</a>`) || !strings.Contains(c, ">Открыть в Ripperr</a>") {
		t.Errorf("retry caption %q", c)
	}
}

func TestTelegramPhotoURLCarriesMarkupAndFallsBack(t *testing.T) {
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer img.Close()
	_, got := tgServer(t, func(n int, _ tgReq) int {
		if n == 1 {
			return http.StatusBadRequest
		}
		return http.StatusOK
	})
	n := New(Config{TelegramToken: "tok", TelegramChatID: "42"}, "").WithAppURL(testAppURL)
	n.posterHC = img.Client()
	if err := n.Send(context.Background(), UpdatedEvent("Name", "https://t/1", img.URL+"/p.jpg")); err != nil {
		t.Fatal(err)
	}
	reqs := got()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	if rows := tgButtonsOf(t, reqs[0].body["reply_markup"]); len(rows) != 1 || len(rows[0]) != 2 {
		t.Errorf("first inline_keyboard = %+v", rows)
	}
	if !strings.HasSuffix(reqs[1].path, "/sendPhoto") || reqs[1].body["photo"] != img.URL+"/p.jpg" {
		t.Errorf("retry path %q photo %v", reqs[1].path, reqs[1].body["photo"])
	}
	if _, ok := reqs[1].body["reply_markup"]; ok {
		t.Error("retry carries reply_markup")
	}
	if c, _ := reqs[1].body["caption"].(string); !strings.Contains(c, `<a href="https://t/1">Topic on tracker</a>`) {
		t.Errorf("retry caption %q", c)
	}
}
