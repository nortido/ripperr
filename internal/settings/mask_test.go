package settings

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/storage/client/sqlite"
)

const seedIntegrationsDoc = `{
	"client": "qbit",
	"qbit": {"host": "http://hu:SEC-qh@qbit:8080/qb/", "username": "qu", "password": "SEC-qp"},
	"transmission": {"host": "http://tu:SEC-th@tm:9091/transmission/rpc", "username": "tu", "password": "SEC-tp"},
	"deluge": {"host": "http://du:SEC-dh@dl:8112", "password": "SEC-dp"},
	"flaresolverr": {"url": "http://fu:SEC-fp@flare:8191", "timeout_ms": 1000},
	"proxy": {"download_proxy": "socks5://pu:SEC-pp@proxy:1080"},
	"notifications": {
		"telegram": {"bot_token": "SEC-tg", "chat_id": "12345", "events": ["check_failed"]},
		"webhook": {"url": "https://wu:SEC-wp@hooks.example:8443/SEC-wpath?t=SEC-wq#SEC-wf", "events": []},
		"email": {"host": "smtp.example.com", "port": 587, "security": "starttls", "username": "eu", "password": "SEC-ep", "from": "bot@example.com", "to": "a@example.com"},
		"pushover": {"app_token": "SEC-pa", "user_key": "SEC-pu"},
		"enabled": true
	},
	"metrics": {"token": "SEC-mt"}
}`

var secretPaths = []string{
	"qbit.host", "qbit.password",
	"transmission.host", "transmission.password",
	"deluge.host", "deluge.password",
	"flaresolverr.url", "proxy.download_proxy",
	"notifications.telegram.bot_token", "notifications.webhook.url",
	"notifications.email.password",
	"notifications.pushover.app_token", "notifications.pushover.user_key",
	"metrics.token",
}

func seedOverlay(t *testing.T) integrationsOverlay {
	t.Helper()
	var o integrationsOverlay
	if err := json.Unmarshal([]byte(seedIntegrationsDoc), &o); err != nil {
		t.Fatal(err)
	}
	return o
}

func seedApp(t *testing.T) config.App {
	t.Helper()
	var cfg config.App
	applyIntegrations(&cfg, seedOverlay(t))
	return cfg
}

func jsonAt(t *testing.T, doc map[string]any, path string) any {
	t.Helper()
	var cur any = doc
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%s: not an object at %q", path, part)
		}
		if cur, ok = m[part]; !ok {
			t.Fatalf("%s: missing key %q", path, part)
		}
	}
	return cur
}

func TestIntegrationsJSONMasksEverySecret(t *testing.T) {
	cfg := seedApp(t)
	before := cfg
	raw := IntegrationsJSON(&cfg)
	if !reflect.DeepEqual(cfg, before) {
		t.Fatal("IntegrationsJSON modified its argument")
	}
	if strings.Contains(string(raw), "SEC-") {
		t.Errorf("secret leaked: %s", raw)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"qbit.host":                        "http://********@qbit:8080/qb/",
		"qbit.password":                    "********",
		"qbit.username":                    "qu",
		"transmission.host":                "http://********@tm:9091/transmission/rpc",
		"transmission.password":            "********",
		"transmission.username":            "tu",
		"deluge.host":                      "http://********@dl:8112",
		"deluge.password":                  "********",
		"flaresolverr.url":                 "http://********@flare:8191",
		"flaresolverr.timeout_ms":          float64(1000),
		"proxy.download_proxy":             "socks5://********@proxy:1080",
		"notifications.telegram.bot_token": "********",
		"notifications.telegram.chat_id":   "12345",
		"notifications.telegram.events":    []any{"check_failed"},
		"notifications.webhook.url":        "https://hooks.example:8443/********",
		"notifications.webhook.events":     []any{},
		"notifications.email.host":         "smtp.example.com",
		"notifications.email.port":         float64(587),
		"notifications.email.security":     "starttls",
		"notifications.email.username":     "eu",
		"notifications.email.password":     "********",
		"notifications.email.from":         "bot@example.com",
		"notifications.email.to":           "a@example.com",
		"notifications.pushover.app_token": "********",
		"notifications.pushover.user_key":  "********",
		"notifications.enabled":            true,
		"metrics.token":                    "********",
		"client":                           "qbit",
	}
	for path, w := range want {
		if got := jsonAt(t, doc, path); !reflect.DeepEqual(got, w) {
			t.Errorf("%s = %#v, want %#v", path, got, w)
		}
	}

	var zero config.App
	if err := json.Unmarshal(IntegrationsJSON(&zero), &doc); err != nil {
		t.Fatal(err)
	}
	for _, path := range secretPaths {
		if got := jsonAt(t, doc, path); got != "" {
			t.Errorf("zero cfg %s = %#v, want empty", path, got)
		}
	}
}

func TestMaskURLForms(t *testing.T) {
	cases := []struct {
		kind secretKind
		in   string
		want string
	}{
		{secretPlain, "", ""},
		{secretPlain, "pw", "********"},
		{secretHost, "", ""},
		{secretHost, "http://qbit:8080/qb/", "http://qbit:8080/qb/"},
		{secretHost, "http://u:p@[::1]:8080", "http://********@[::1]:8080"},
		{secretHost, "http://u@h", "http://********@h"},
		{secretHost, "http://u:p*x@h:1/a*b?q=1#f", "http://********@h:1/a*b?q=1#f"},
		{secretHost, "http://", "********"},
		{secretHost, "http://u:p@host:bad", "********"},
		{secretHost, "::bad", "********"},
		{secretWebhook, "", ""},
		{secretWebhook, "https://h.example", "https://h.example"},
		{secretWebhook, "https://h.example/", "https://h.example/"},
		{secretWebhook, "https://h.example?t=1", "https://h.example/********"},
		{secretWebhook, "https://u:p@h.example", "https://h.example/********"},
		{secretWebhook, "https://h.example#frag", "https://h.example/********"},
		{secretWebhook, "https://h.example:8443/a/b", "https://h.example:8443/********"},
		{secretWebhook, "not a url", "********"},
		{secretWebhook, "https://h:bad/x", "********"},
	}
	for _, c := range cases {
		got := maskValue(c.kind, c.in)
		if got != c.want {
			t.Errorf("maskValue(%d, %q) = %q, want %q", c.kind, c.in, got, c.want)
		}
		if strings.Contains(got, "%2A") {
			t.Errorf("maskValue(%d, %q) = %q has %%2A", c.kind, c.in, got)
		}
	}
	if MaskSecret("x") != SecretMask || MaskSecret("") != "" || SecretMask != "********" {
		t.Errorf("MaskSecret: %q %q %q", MaskSecret("x"), MaskSecret(""), SecretMask)
	}
}

func TestUnmaskIntegrationsPatchKeepsMaskedRoundTrip(t *testing.T) {
	cur := seedApp(t)
	body := IntegrationsJSON(&cur)
	out, err := UnmaskIntegrationsPatch(body, &cur)
	if err != nil {
		t.Fatal(err)
	}
	got, err := IntegrationsPatchFromJSON(out)
	if err != nil {
		t.Fatal(err)
	}
	want, err := IntegrationsPatchFromJSON(body)
	if err != nil {
		t.Fatal(err)
	}
	if want.Qbit.Username == nil || want.Notifications.Email.Port == nil {
		t.Fatal("body lost non-secret fields")
	}
	for _, f := range secretFields {
		if p := *f.overlay(&got); p != nil {
			t.Errorf("%s = %q, want nil (keep)", f.path, *p)
		}
		*f.overlay(&want) = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("non-secret fields changed:\n got %+v\nwant %+v", got, want)
	}
}

func TestMergeIntegrationsMaskedRoundTripKeepsStored(t *testing.T) {
	ctx := context.Background()
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		sqlite.SettingIntegrations: json.RawMessage(seedIntegrationsDoc),
	}}
	var cur config.App
	if err := ApplyOverrides(ctx, &cur, db); err != nil {
		t.Fatal(err)
	}
	out, err := UnmaskIntegrationsPatch(IntegrationsJSON(&cur), &cur)
	if err != nil {
		t.Fatal(err)
	}
	if err = MergeIntegrations(ctx, db, out); err != nil {
		t.Fatal(err)
	}
	var saved integrationsOverlay
	if err = json.Unmarshal(db.data[sqlite.SettingIntegrations], &saved); err != nil {
		t.Fatal(err)
	}
	seed := seedOverlay(t)
	for _, f := range secretFields {
		got, want := *f.overlay(&saved), *f.overlay(&seed)
		if got == nil || *got != *want {
			t.Errorf("%s: stored value changed", f.path)
		}
	}
}

func TestUnmaskIntegrationsPatchSetClearReject(t *testing.T) {
	cur := seedApp(t)
	unmask := func(t *testing.T, cur *config.App, body string) integrationsOverlay {
		t.Helper()
		out, err := UnmaskIntegrationsPatch(json.RawMessage(body), cur)
		if err != nil {
			t.Fatal(err)
		}
		var o integrationsOverlay
		if err = json.Unmarshal(out, &o); err != nil {
			t.Fatal(err)
		}
		return o
	}

	o := unmask(t, &cur, `{"qbit":{"password":"new-pw"},"notifications":{"webhook":{"url":"https://new.example/hook"}}}`)
	if o.Qbit.Password == nil || *o.Qbit.Password != "new-pw" {
		t.Errorf("literal password = %v", o.Qbit.Password)
	}
	if o.Notifications.Webhook.URL == nil || *o.Notifications.Webhook.URL != "https://new.example/hook" {
		t.Errorf("literal webhook = %v", o.Notifications.Webhook.URL)
	}

	o = unmask(t, &cur, `{"qbit":{"password":""},"notifications":{"webhook":{"url":""}},"proxy":{"download_proxy":""}}`)
	for path, p := range map[string]*string{
		"qbit.password":             o.Qbit.Password,
		"notifications.webhook.url": o.Notifications.Webhook.URL,
		"proxy.download_proxy":      o.Proxy.DownloadProxy,
	} {
		if p == nil || *p != "" {
			t.Errorf("%s: clear must pass through, got %v", path, p)
		}
	}

	var empty config.App
	if o = unmask(t, &empty, `{"metrics":{"token":"********"}}`); o.Metrics.Token != nil {
		t.Errorf("mask over empty stored = %q, want nil", *o.Metrics.Token)
	}

	for _, c := range []struct{ path, body string }{
		{"qbit.password", `{"qbit":{"password":"x********"}}`},
		{"notifications.webhook.url", `{"notifications":{"webhook":{"url":"https://other.example/********"}}}`},
		{"proxy.download_proxy", `{"proxy":{"download_proxy":"socks5://********@proxy:1081"}}`},
	} {
		_, err := UnmaskIntegrationsPatch(json.RawMessage(c.body), &cur)
		if err == nil {
			t.Errorf("%s: expected error", c.path)
			continue
		}
		if msg := err.Error(); !strings.Contains(msg, c.path) || strings.Contains(msg, "SEC-") || strings.Contains(msg, "other.example") || strings.Contains(msg, "1081") {
			t.Errorf("%s: error %q", c.path, msg)
		}
	}
}

func TestRestoreMaskedSecrets(t *testing.T) {
	cur := seedApp(t)
	dst := cur
	for _, f := range secretFields {
		p := f.app(&dst)
		*p = maskValue(f.kind, *p)
	}
	if err := RestoreMaskedSecrets(&dst, &cur); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dst, cur) {
		t.Errorf("restore:\n got %+v\nwant %+v", dst, cur)
	}

	dst = cur
	dst.Manager.QbitTorrent.Password = ""
	dst.Notify.TelegramToken = "new-token"
	if err := RestoreMaskedSecrets(&dst, &cur); err != nil {
		t.Fatal(err)
	}
	if dst.Manager.QbitTorrent.Password != "" || dst.Notify.TelegramToken != "new-token" {
		t.Errorf("clear/literal: %q %q", dst.Manager.QbitTorrent.Password, dst.Notify.TelegramToken)
	}

	dst = cur
	dst.Notify.WebhookURL = "https://other.example/********"
	err := RestoreMaskedSecrets(&dst, &cur)
	if err == nil || !strings.Contains(err.Error(), "notifications.webhook.url") || strings.Contains(err.Error(), "SEC-") {
		t.Errorf("partial: %v", err)
	}

	cur.Metrics.Token = "ab********cd"
	dst = cur
	if err = RestoreMaskedSecrets(&dst, &cur); err != nil {
		t.Fatalf("stored value with marker: %v", err)
	}
	if dst.Metrics.Token != "ab********cd" {
		t.Errorf("stored value with marker = %q", dst.Metrics.Token)
	}
}

func TestRevealIntegrationSecret(t *testing.T) {
	cfg := seedApp(t)
	plain := map[string]string{
		"qbit.password":                    "SEC-qp",
		"transmission.password":            "SEC-tp",
		"deluge.password":                  "SEC-dp",
		"notifications.telegram.bot_token": "SEC-tg",
		"notifications.email.password":     "SEC-ep",
		"notifications.pushover.app_token": "SEC-pa",
		"notifications.pushover.user_key":  "SEC-pu",
		"metrics.token":                    "SEC-mt",
	}
	for path, want := range plain {
		if got, ok := RevealIntegrationSecret(&cfg, path); !ok || got != want {
			t.Errorf("%s = %q, %t, want %q, true", path, got, ok, want)
		}
	}
	for _, path := range []string{
		"qbit.host", "transmission.host", "deluge.host", "flaresolverr.url",
		"proxy.download_proxy", "notifications.webhook.url", "nope", "",
	} {
		if got, ok := RevealIntegrationSecret(&cfg, path); ok || got != "" {
			t.Errorf("%q = %q, %t, want empty, false", path, got, ok)
		}
	}
}
