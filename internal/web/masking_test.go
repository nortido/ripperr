package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/helper/formcreds"
	"ripperr/internal/notify"
	"ripperr/internal/runner"
	"ripperr/internal/settings"
	"ripperr/internal/tracker/registry"
	"ripperr/internal/webpush"
)

const maskingIntegrationsSeed = `{"client":"qbit",
"qbit":{"host":"http://qu:SEC-qh@127.0.0.1:1","username":"qu","password":"SEC-qp"},
"transmission":{"host":"http://tu:SEC-th@127.0.0.1:1","username":"tu","password":"SEC-tp"},
"deluge":{"host":"http://du:SEC-dh@127.0.0.1:1","password":"SEC-dp"},
"downloadstation":{"host":"http://su:SEC-sh@127.0.0.1:1","username":"su","password":"SEC-sp"},
"flaresolverr":{"url":"http://fu:SEC-fh@127.0.0.1:1","timeout_ms":1000},
"proxy":{"download_proxy":"socks5://pu:SEC-pp@127.0.0.1:1"},
"notifications":{"telegram":{"bot_token":"SEC-tg","chat_id":"42"},
"webhook":{"url":"http://127.0.0.1:1/SEC-wp?k=SEC-wq"},
"email":{"host":"127.0.0.1","port":1,"security":"none","username":"eu","password":"SEC-ep","from":"a@b.c","to":"d@e.f"},
"pushover":{"app_token":"SEC-pa","user_key":"SEC-pu"},"enabled":true},
"metrics":{"token":"SEC-mt"}}`

var maskingSecretPaths = []string{
	"qbit.host", "qbit.password",
	"transmission.host", "transmission.password",
	"deluge.host", "deluge.password",
	"downloadstation.host", "downloadstation.password",
	"flaresolverr.url", "proxy.download_proxy",
	"notifications.telegram.bot_token", "notifications.telegram.chat_id",
	"notifications.webhook.url", "notifications.email.password",
	"notifications.pushover.app_token", "notifications.pushover.user_key",
	"metrics.token",
}

func newMaskingServer(store *fakeStore) *Server {
	return withTestSession(New(store, func() {}, func() config.App {
		cfg := testEffective()
		cfg.Tracker = registry.ConfigDefaults()
		if err := settings.ApplyOverrides(context.Background(), &cfg, store); err != nil {
			panic(err)
		}
		return cfg
	}, ":0", runner.NewState(), nil))
}

func jsonPath(t *testing.T, raw json.RawMessage, path string) string {
	t.Helper()
	var cur any
	if err := json.Unmarshal(raw, &cur); err != nil {
		t.Fatal(err)
	}
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[part]
	}
	s, _ := cur.(string)
	return s
}

func stubTelegramResolver(t *testing.T) *int {
	t.Helper()
	orig := telegramChatIDResolver
	t.Cleanup(func() { telegramChatIDResolver = orig })
	calls := new(int)
	telegramChatIDResolver = func(context.Context, string, string) (string, error) {
		*calls++
		return "99", nil
	}
	return calls
}

func getIntegrations(t *testing.T, srv *Server) []byte {
	t.Helper()
	w := httptest.NewRecorder()
	srv.handleGetIntegrations(w, httptest.NewRequest(http.MethodGet, "/api/integrations", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET integrations status = %d", w.Code)
	}
	return w.Body.Bytes()
}

func putIntegrations(srv *Server, body []byte) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	srv.handlePutIntegrations(w, httptest.NewRequest(http.MethodPut, "/api/integrations", bytes.NewReader(body)))
	return w
}

func TestGetTrackersHidesSecrets(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"credentials":"login_username=vasya&login_password=SEC-trp","cookies":"SEC-trc"},"nnm":{"cookies":"SEC-nnm"}}`),
	}}
	srv := newMaskingServer(store)
	w := httptest.NewRecorder()
	srv.handleGetTrackers(w, httptest.NewRequest(http.MethodGet, "/api/trackers", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "SEC-") {
		t.Fatalf("GET /api/trackers leaks a secret: %s", w.Body.String())
	}
	var resp []struct {
		Key         string `json:"key"`
		Login       string `json:"login"`
		Password    string `json:"password"`
		Credentials string `json:"credentials"`
		Cookies     string `json:"cookies"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, it := range resp {
		switch it.Key {
		case "rutracker":
			seen++
			if it.Login != "vasya" || it.Password != settings.SecretMask || it.Credentials != "login_username={login}&login_password={password}" || it.Cookies != settings.SecretMask {
				t.Errorf("rutracker = %+v, want login vasya, the template and masked password, cookies", it)
			}
		case "nnm":
			seen++
			if it.Cookies != settings.SecretMask {
				t.Errorf("nnm cookies = %q, want mask", it.Cookies)
			}
		case "kinozal":
			seen++
			if it.Password != "" || it.Credentials != "username={login}&password={password}&to=%2F" || it.Cookies != "" {
				t.Errorf("kinozal = %+v, want empty secrets and the registry template", it)
			}
		}
	}
	if seen != 3 {
		t.Fatalf("rutracker, nnm or kinozal missing: %s", w.Body.String())
	}
}

func TestPutIntegrationsMaskedRoundTripKeepsSecrets(t *testing.T) {
	calls := stubTelegramResolver(t)
	seed := json.RawMessage(maskingIntegrationsSeed)
	store := &fakeStore{settings: map[string]json.RawMessage{"integrations": seed}}
	srv := newMaskingServer(store)

	w := putIntegrations(srv, getIntegrations(t, srv))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}
	for _, p := range maskingSecretPaths {
		if got, want := jsonPath(t, store.settings["integrations"], p), jsonPath(t, seed, p); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	if *calls != 0 {
		t.Errorf("telegram resolver calls = %d, want 0", *calls)
	}
}

func TestPutIntegrationsRejectsPartialMask(t *testing.T) {
	stubTelegramResolver(t)
	seed := json.RawMessage(maskingIntegrationsSeed)
	store := &fakeStore{settings: map[string]json.RawMessage{"integrations": seed}}
	srv := newMaskingServer(store)

	w := putIntegrations(srv, []byte(`{"notifications":{"webhook":{"url":"https://evil.example/********"}}}`))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "SEC-") {
		t.Errorf("error body leaks a secret: %s", w.Body.String())
	}
	if !bytes.Equal(store.settings["integrations"], seed) {
		t.Errorf("stored doc changed: %s", store.settings["integrations"])
	}
}

func TestPutIntegrationsExplicitClear(t *testing.T) {
	calls := stubTelegramResolver(t)
	store := &fakeStore{settings: map[string]json.RawMessage{"integrations": json.RawMessage(maskingIntegrationsSeed)}}
	srv := newMaskingServer(store)

	w := putIntegrations(srv, []byte(`{"qbit":{"password":""},"notifications":{"telegram":{"bot_token":""}}}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body: %s", w.Code, w.Body.String())
	}
	for _, p := range []string{"qbit.password", "notifications.telegram.bot_token", "notifications.telegram.chat_id"} {
		if got := jsonPath(t, store.settings["integrations"], p); got != "" {
			t.Errorf("%s = %q, want empty", p, got)
		}
	}
	if *calls != 0 {
		t.Errorf("telegram resolver calls = %d, want 0", *calls)
	}
}

func TestIntegrationTestUsesStoredSecretsWhenMasked(t *testing.T) {
	calls := stubTelegramResolver(t)
	store := &fakeStore{settings: map[string]json.RawMessage{"integrations": json.RawMessage(maskingIntegrationsSeed)}}
	srv := newMaskingServer(store)
	eff := srv.effective()
	var masked map[string]json.RawMessage
	if err := json.Unmarshal(getIntegrations(t, srv), &masked); err != nil {
		t.Fatal(err)
	}
	part := func(kind string, edit func(map[string]any)) json.RawMessage {
		src := kind
		if kind == "notify" {
			src = "notifications"
		}
		var m map[string]any
		_ = json.Unmarshal(masked[src], &m)
		if kind == "notify" {
			m["channel"] = ""
		}
		if edit != nil {
			edit(m)
		}
		out, _ := json.Marshal(m)
		return out
	}
	type ref struct {
		name string
		get  func(*config.App) string
	}
	kinds := map[string][]ref{
		"qbit": {
			{"host", func(c *config.App) string { return c.Manager.QbitTorrent.Host }},
			{"password", func(c *config.App) string { return c.Manager.QbitTorrent.Password }},
		},
		"transmission": {
			{"host", func(c *config.App) string { return c.Manager.Transmission.Host }},
			{"password", func(c *config.App) string { return c.Manager.Transmission.Password }},
		},
		"deluge": {
			{"host", func(c *config.App) string { return c.Manager.Deluge.Host }},
			{"password", func(c *config.App) string { return c.Manager.Deluge.Password }},
		},
		"downloadstation": {
			{"host", func(c *config.App) string { return c.Manager.DownloadStation.Host }},
			{"password", func(c *config.App) string { return c.Manager.DownloadStation.Password }},
		},
		"flaresolverr": {{"url", func(c *config.App) string { return c.Flaresolverr.Host }}},
		"proxy":        {{"download_proxy", func(c *config.App) string { return c.Runner.DownloadProxy }}},
		"notify": {
			{"bot_token", func(c *config.App) string { return c.Notify.TelegramToken }},
			{"chat_id", func(c *config.App) string { return c.Notify.TelegramChatID }},
			{"webhook", func(c *config.App) string { return c.Notify.WebhookURL }},
			{"email.password", func(c *config.App) string { return c.Notify.EmailPassword }},
			{"app_token", func(c *config.App) string { return c.Notify.PushoverToken }},
			{"user_key", func(c *config.App) string { return c.Notify.PushoverUser }},
		},
	}
	for kind, refs := range kinds {
		got, err := mergeTestIntegrationsConfig(eff, kind, part(kind, nil))
		if err != nil {
			t.Errorf("%s: merge error %v", kind, err)
			continue
		}
		for _, r := range refs {
			if g, want := r.get(&got), r.get(&eff); g != want || !strings.Contains(g, "SEC-") && r.name != "chat_id" {
				t.Errorf("%s.%s = %q, want stored %q", kind, r.name, g, want)
			}
		}
	}

	blanks := []struct {
		kind string
		edit func(map[string]any)
		get  func(*config.App) string
	}{
		{"qbit", func(m map[string]any) { m["password"] = "" }, func(c *config.App) string { return c.Manager.QbitTorrent.Password }},
		{"deluge", func(m map[string]any) { m["password"] = "" }, func(c *config.App) string { return c.Manager.Deluge.Password }},
		{"downloadstation", func(m map[string]any) { m["password"] = "" }, func(c *config.App) string { return c.Manager.DownloadStation.Password }},
		{"proxy", func(m map[string]any) { m["download_proxy"] = "" }, func(c *config.App) string { return c.Runner.DownloadProxy }},
		{"notify", func(m map[string]any) { m["telegram"].(map[string]any)["bot_token"] = "" }, func(c *config.App) string { return c.Notify.TelegramToken }},
	}
	for _, b := range blanks {
		got, err := mergeTestIntegrationsConfig(eff, b.kind, part(b.kind, b.edit))
		if err != nil {
			t.Errorf("%s blank: merge error %v", b.kind, err)
			continue
		}
		if g := b.get(&got); g != "" {
			t.Errorf("%s blank = %q, want empty", b.kind, g)
		}
	}

	origSend := sendNotification
	t.Cleanup(func() { sendNotification = origSend })
	var sent notify.Config
	sendNotification = func(_ context.Context, cfg notify.Config, _ string, _ *webpush.Store, _ notify.Event) error {
		sent = cfg
		return nil
	}
	cfgJSON := part("notify", func(m map[string]any) {
		m["channel"] = "telegram"
		delete(m["telegram"].(map[string]any), "chat_id")
	})
	body, _ := json.Marshal(map[string]any{"kind": "notify", "config": json.RawMessage(cfgJSON)})
	w := httptest.NewRecorder()
	srv.handleTestIntegration(w, httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("notify test status = %d, body: %s", w.Code, w.Body.String())
	}
	if sent.TelegramToken != "SEC-tg" || sent.TelegramChatID != "42" {
		t.Errorf("sent token/chat = %q/%q, want SEC-tg/42", sent.TelegramToken, sent.TelegramChatID)
	}
	if *calls != 0 {
		t.Errorf("telegram resolver calls = %d, want 0", *calls)
	}
}

const maskingTrackerCreds = "login_username=vasya&login_password=SEC-trp&extra=1"

func newMaskingTrackerStore() *fakeStore {
	return &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"credentials":"` + maskingTrackerCreds + `","cookies":"SEC-trc"}}`),
	}}
}

func maskedTrackerForm(t *testing.T, srv *Server, key string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	srv.handleGetTrackers(w, httptest.NewRequest(http.MethodGet, "/api/trackers", nil))
	var items []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	str := func(v any) string { s, _ := v.(string); return s }
	num := func(v any) float64 { n, _ := v.(float64); return n }
	for _, it := range items {
		if it["key"] != key {
			continue
		}
		publicity := str(it["publicity"])
		if publicity == "" {
			publicity = "public"
		}
		mirrors, _ := it["mirrors"].([]any)
		if mirrors == nil {
			mirrors = []any{}
		}
		form := map[string]any{
			"enabled": it["enabled"] == true, "domain": str(it["domain"]),
			"hour_limit": num(it["hour_limit"]), "proxy_type": num(it["proxy_type"]),
			"login_url": str(it["login_url"]), "download_url": str(it["download_url"]),
			"credentials": str(it["credentials"]), "cookies": settings.MaskSecret(str(it["cookies"])),
			"publicity": publicity, "mirrors": mirrors, "active_domain": str(it["active_domain"]),
		}
		if it["supports_login"] == true {
			form["login"], form["password"] = str(it["login"]), settings.MaskSecret(str(it["password"]))
		}
		return form
	}
	t.Fatalf("tracker %s missing", key)
	return nil
}

func TestTrackerPutMaskedKeepsStored(t *testing.T) {
	cases := []struct {
		name        string
		edit        func(map[string]any)
		wantStatus  int
		credentials string
		cookies     string
	}{
		{"verbatim", nil, http.StatusNoContent, maskingTrackerCreds, "SEC-trc"},
		{"new password", func(m map[string]any) { m["password"] = "newpass" }, http.StatusNoContent, "login_username=vasya&login_password=newpass&extra=1", "SEC-trc"},
		{"blank password", func(m map[string]any) { m["password"] = "" }, http.StatusNoContent, "login_username=vasya&login_password=&extra=1", "SEC-trc"},
		{"blank cookies", func(m map[string]any) { m["cookies"] = "" }, http.StatusNoContent, maskingTrackerCreds, ""},
		{"partial cookies", func(m map[string]any) { m["cookies"] = "a********" }, http.StatusBadRequest, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMaskingTrackerStore()
			before := string(store.settings["trackers"])
			srv := newMaskingServer(store)
			form := maskedTrackerForm(t, srv, "rutracker")
			if tc.edit != nil {
				tc.edit(form)
			}
			body, _ := json.Marshal(form)
			req := httptest.NewRequest(http.MethodPut, "/api/trackers/rutracker", bytes.NewReader(body))
			req.SetPathValue("key", "rutracker")
			w := httptest.NewRecorder()
			srv.handlePutTracker(w, req)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body: %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantStatus != http.StatusNoContent {
				if strings.Contains(w.Body.String(), "SEC-") {
					t.Errorf("error body leaks a secret: %s", w.Body.String())
				}
				if string(store.settings["trackers"]) != before {
					t.Errorf("stored trackers changed: %s", store.settings["trackers"])
				}
				return
			}
			var saved map[string]map[string]any
			if err := json.Unmarshal(store.settings["trackers"], &saved); err != nil {
				t.Fatal(err)
			}
			assertStoredTemplate(t, store, "rutracker", maskingTrackerTemplate, tc.credentials)
			if got := saved["rutracker"]["cookies"]; got != tc.cookies {
				t.Errorf("cookies = %q, want %q", got, tc.cookies)
			}
		})
	}
}

func TestIntegrationErrorsHideSecrets(t *testing.T) {
	resolverCalls := stubTelegramResolver(t)
	closed := httptest.NewServer(http.NotFoundHandler())
	webhook := closed.URL + "/SEC-wp?k=SEC-wq"
	closed.Close()
	seed := `{"client":"qbit",
"qbit":{"host":"http://u:SEC-qh@127.0.0.1:1","username":"u","password":"SEC-qp"},
"transmission":{"host":"http://u:SEC-th@127.0.0.1:1","username":"u","password":"SEC-tp"},
"deluge":{"host":"http://u:SEC-dh@127.0.0.1:1","password":"SEC-dp"},
"downloadstation":{"host":"http://u:SEC-sh@127.0.0.1:1","username":"u","password":"SEC-sp"},
"flaresolverr":{"url":"http://u:SEC-fh@127.0.0.1:1","timeout_ms":1000},
"proxy":{"download_proxy":"socks5://u:SEC-pp@127.0.0.1:1"},
"notifications":{"webhook":{"url":"` + webhook + `"},"enabled":true}}`
	store := &fakeStore{settings: map[string]json.RawMessage{"integrations": json.RawMessage(seed)}}
	srv := newMaskingServer(store)
	var masked map[string]json.RawMessage
	if err := json.Unmarshal(getIntegrations(t, srv), &masked); err != nil {
		t.Fatal(err)
	}

	for _, kind := range []string{"qbit", "transmission", "deluge", "downloadstation", "flaresolverr", "proxy", "notify"} {
		src := kind
		if kind == "notify" {
			src = "notifications"
		}
		var notifyPart map[string]any
		_ = json.Unmarshal(masked[src], &notifyPart)
		if kind == "notify" {
			notifyPart["channel"] = "webhook"
		}
		maskedCfg, _ := json.Marshal(notifyPart)
		for _, cfg := range []json.RawMessage{nil, maskedCfg} {
			body, _ := json.Marshal(map[string]any{"kind": kind, "config": cfg})
			w := httptest.NewRecorder()
			srv.handleTestIntegration(w, httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader(body)))
			if strings.Contains(w.Body.String(), "SEC-") {
				t.Errorf("%s (config %t) leaks a secret: %d %s", kind, cfg != nil, w.Code, w.Body.String())
			}
		}
	}
	if *resolverCalls != 0 {
		t.Errorf("telegram resolver calls = %d, want 0", *resolverCalls)
	}

	w := httptest.NewRecorder()
	srv.handleGetCategories(w, httptest.NewRequest(http.MethodGet, "/api/categories", nil))
	if w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "SEC-") {
		t.Errorf("categories = %d %s, want 502 without secrets", w.Code, w.Body.String())
	}

	w = putIntegrations(srv, []byte(`{"qbit":{"host":"http://u:SEC-x@host:bad"}}`))
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "SEC-") {
		t.Errorf("PUT bad host = %d %s, want 400 without secrets", w.Code, w.Body.String())
	}
}

func TestTrackerTestMaskedUsesStored(t *testing.T) {
	store := newMaskingTrackerStore()
	srv := newMaskingServer(store)
	eff := srv.effective()
	body, _ := json.Marshal(maskedTrackerForm(t, srv, "rutracker"))

	patched, err := prepareTrackerPatch(eff, "rutracker", body)
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := settings.TrackerPatchFromJSON(patched)
	if err != nil {
		t.Fatal(err)
	}
	cfg := eff
	cfg.Tracker = eff.Tracker.Clone()
	settings.ApplyTrackerKey(&cfg, "rutracker", overlay)
	it := cfg.Tracker.ByKey("rutracker")
	if it.Credentials != maskingTrackerTemplate || formcreds.Expand(it.Credentials, it.Login, it.Password) != maskingTrackerCreds || it.Cookies != "SEC-trc" {
		t.Errorf("credentials/login/cookies = %q/%q/%q, want stored", it.Credentials, it.Login, it.Cookies)
	}
}

func TestTrackerPutMaskedPasswordWithBlankCredentialsKeepsPassword(t *testing.T) {
	const creds = "username=vasya&password=SEC-kp&to=%2F"
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"kinozal":{"credentials":"` + creds + `"}}`),
	}}
	srv := newMaskingServer(store)
	body := []byte(`{"login":"vasya","password":"********","credentials":"","cookies":"********"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/kinozal", bytes.NewReader(body))
	req.SetPathValue("key", "kinozal")
	w := httptest.NewRecorder()
	srv.handlePutTracker(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	assertStoredTemplate(t, store, "kinozal", "username={login}&password={password}&to=%2F", creds)
}

func TestPutIntegrationsMaskedEnvTokenResolvesMissingChatID(t *testing.T) {
	orig := telegramChatIDResolver
	t.Cleanup(func() { telegramChatIDResolver = orig })
	var tokens []string
	telegramChatIDResolver = func(_ context.Context, token, _ string) (string, error) {
		tokens = append(tokens, token)
		return "77", nil
	}
	store := &fakeStore{}
	srv := New(store, func() {}, func() config.App {
		cfg := testEffective()
		cfg.Notify.TelegramToken = "SEC-envtg"
		if err := settings.ApplyOverrides(context.Background(), &cfg, store); err != nil {
			panic(err)
		}
		return cfg
	}, ":0", runner.NewState(), nil)

	w := putIntegrations(srv, getIntegrations(t, srv))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	if len(tokens) != 1 || tokens[0] != "SEC-envtg" {
		t.Errorf("resolver tokens = %q, want one call with the env token", tokens)
	}
	if got := jsonPath(t, store.settings["integrations"], "notifications.telegram.chat_id"); got != "77" {
		t.Errorf("stored chat_id = %q, want 77", got)
	}
	if got := jsonPath(t, store.settings["integrations"], "notifications.telegram.bot_token"); got != "" {
		t.Errorf("stored bot_token = %q, want the env token left out of the DB", got)
	}
}

const maskingTrackerTemplate = "login_username={login}&login_password={password}&extra=1"

func putTrackerJSON(srv *Server, key string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/api/trackers/"+key, bytes.NewReader(body))
	req.SetPathValue("key", key)
	w := httptest.NewRecorder()
	srv.handlePutTracker(w, req)
	return w
}

func storedTrackerLogin(t *testing.T, store *fakeStore, key string) (credentials, login, password string) {
	t.Helper()
	var saved map[string]map[string]any
	if err := json.Unmarshal(store.settings["trackers"], &saved); err != nil {
		t.Fatal(err)
	}
	credentials, _ = saved[key]["credentials"].(string)
	login, _ = saved[key]["login"].(string)
	password, _ = saved[key]["password"].(string)
	return credentials, login, password
}

func TestPutTrackerStoresTemplateAndSeparateLogin(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"credentials":"login=%D0%B2%D1%85%D0%BE%D0%B4&login_password=old&login_username=old"}}`),
	}}
	srv := newMaskingServer(store)
	w := putTrackerJSON(srv, "rutracker", []byte(`{"login":"va&sya","password":"p w%{}"}`))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	creds, login, password := storedTrackerLogin(t, store, "rutracker")
	if want := "login=%D0%B2%D1%85%D0%BE%D0%B4&login_password={password}&login_username={login}"; creds != want {
		t.Errorf("credentials = %q, want %q", creds, want)
	}
	if login != "va&sya" || password != "p w%{}" {
		t.Errorf("login/password = %q/%q, want va&sya/p w%%{}", login, password)
	}
	if got, want := formcreds.Expand(creds, login, password), "login=%D0%B2%D1%85%D0%BE%D0%B4&login_password=p+w%25%7B%7D&login_username=va%26sya"; got != want {
		t.Errorf("Expand = %q, want %q", got, want)
	}
}

func TestGetTrackersShowsCredentialsTemplate(t *testing.T) {
	const tmpl = "login=%D0%B2%D1%85%D0%BE%D0%B4&login_password={password}&login_username={login}"
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"credentials":"` + tmpl + `","login":"vasya","password":"SEC-p"},"nnm":{"credentials":"SEC-nnmraw"}}`),
	}}
	type item struct {
		Key         string `json:"key"`
		Login       string `json:"login"`
		Password    string `json:"password"`
		Credentials string `json:"credentials"`
	}
	get := func(srv *Server) map[string]item {
		t.Helper()
		w := httptest.NewRecorder()
		srv.handleGetTrackers(w, httptest.NewRequest(http.MethodGet, "/api/trackers", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d", w.Code)
		}
		if strings.Contains(w.Body.String(), "SEC-") {
			t.Fatalf("GET /api/trackers leaks a secret: %s", w.Body.String())
		}
		var list []item
		if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
			t.Fatal(err)
		}
		out := make(map[string]item, len(list))
		for _, it := range list {
			out[it.Key] = it
		}
		return out
	}

	got := get(newMaskingServer(store))
	if it := got["rutracker"]; it.Credentials != tmpl || it.Login != "vasya" || it.Password != settings.SecretMask {
		t.Errorf("rutracker = %+v, want the template, login vasya and a masked password", it)
	}
	if it := got["kinozal"]; it.Credentials != "username={login}&password={password}&to=%2F" {
		t.Errorf("kinozal credentials = %q, want the registry template", it.Credentials)
	}
	if it := got["nnm"]; it.Credentials != settings.SecretMask {
		t.Errorf("nnm credentials = %q, want mask", it.Credentials)
	}

	env := get(newTrackerTestServer(&fakeStore{}, "rutracker", "login_username=vasya&login_password=SEC-env"))
	if it := env["rutracker"]; it.Credentials != "login_username={login}&login_password={password}" || it.Login != "vasya" || it.Password != settings.SecretMask {
		t.Errorf("env rutracker = %+v, want the template, login vasya and a masked password", it)
	}
}

func TestPutTrackerMaskedPasswordKeepsStoredAnyCredentials(t *testing.T) {
	seeds := map[string]string{
		"legacy":   `{"rutracker":{"credentials":"` + maskingTrackerCreds + `"}}`,
		"template": `{"rutracker":{"credentials":"` + maskingTrackerTemplate + `","login":"vasya","password":"SEC-trp"}}`,
	}
	cases := []struct {
		name        string
		credentials any
	}{
		{"absent", nil},
		{"mask", settings.SecretMask},
		{"blank", ""},
		{"get template", "from GET"},
		{"hand-edited", "login_username={login}&extra=2"},
	}
	for seedName, seed := range seeds {
		for _, tc := range cases {
			t.Run(seedName+"/"+tc.name, func(t *testing.T) {
				store := &fakeStore{settings: map[string]json.RawMessage{"trackers": json.RawMessage(seed)}}
				srv := newMaskingServer(store)
				form := map[string]any{"login": "vasya", "password": settings.SecretMask}
				switch tc.credentials {
				case nil:
				case "from GET":
					form["credentials"] = maskedTrackerForm(t, srv, "rutracker")["credentials"]
				default:
					form["credentials"] = tc.credentials
				}
				body, _ := json.Marshal(form)
				if w := putTrackerJSON(srv, "rutracker", body); w.Code != http.StatusNoContent {
					t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
				}
				creds, login, password := storedTrackerLogin(t, store, "rutracker")
				if password != "SEC-trp" || login != "vasya" {
					t.Errorf("login/password = %q/%q, want vasya/SEC-trp", login, password)
				}
				if got := formcreds.Expand(creds, login, password); !strings.Contains(got, "login_password=SEC-trp") {
					t.Errorf("Expand = %q, want login_password=SEC-trp", got)
				}
			})
		}
	}
}

func TestPutTrackerHandEditedBodyMovesValuesToFields(t *testing.T) {
	store := &fakeStore{settings: map[string]json.RawMessage{
		"trackers": json.RawMessage(`{"rutracker":{"credentials":"` + maskingTrackerTemplate + `","login":"vasya","password":"SEC-trp"}}`),
	}}
	srv := newMaskingServer(store)
	body := []byte(`{"credentials":"login_username=bob&login_password=SEC-x&extra=1","password":"********"}`)
	if w := putTrackerJSON(srv, "rutracker", body); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	creds, login, password := storedTrackerLogin(t, store, "rutracker")
	if creds != maskingTrackerTemplate || login != "bob" || password != "SEC-x" {
		t.Errorf("stored = %q %q %q, want %q bob SEC-x", creds, login, password, maskingTrackerTemplate)
	}
	if strings.Contains(creds, "SEC-") || strings.Contains(creds, "bob") {
		t.Errorf("credentials keep a real value: %q", creds)
	}
}

func assertStoredTemplate(t *testing.T, store *fakeStore, key, wantTmpl, wantExpanded string) {
	t.Helper()
	creds, login, password := storedTrackerLogin(t, store, key)
	if creds != wantTmpl {
		t.Errorf("credentials = %q, want template %q", creds, wantTmpl)
	}
	if got := formcreds.Expand(creds, login, password); got != wantExpanded {
		t.Errorf("Expand = %q, want %q", got, wantExpanded)
	}
}
