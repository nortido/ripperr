package web

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/notify"
	"ripperr/internal/webpush"
)

const chromeMacUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

func pushBody(t *testing.T, endpoint string) string {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString
	return fmt.Sprintf(`{"endpoint":%q,"expirationTime":null,"keys":{"p256dh":%q,"auth":%q}}`, endpoint, enc(k.PublicKey().Bytes()), enc(auth))
}

type pushCall struct {
	method, path, origin, host, body string
	cookie                           *http.Cookie
}

func doPush(srv *Server, c pushCall) *httptest.ResponseRecorder {
	req := httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
	req.Header.Set("X-Ripperr", "1")
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = "192.168.1.5:6410"
	if c.host != "" {
		req.Host = c.host
	}
	req.Header.Set("User-Agent", chromeMacUA)
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	return w
}

type pushStateJSON struct {
	PublicKey string `json:"public_key"`
	Devices   []struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		CreatedAt int64  `json:"created_at"`
	} `json:"devices"`
}

func TestPushAPIFlow(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true, AllowedHosts: []string{"my.keenetic.pro"}})
	cookie := loginCookie(t, srv)
	const endpoint = "https://fcm.googleapis.com/fcm/send/abc123"
	body := pushBody(t, endpoint)

	state := func() (pushStateJSON, string) {
		w := doPush(srv, pushCall{method: "GET", path: "/api/push", cookie: cookie})
		if w.Code != http.StatusOK {
			t.Fatalf("GET /api/push = %d: %s", w.Code, w.Body.String())
		}
		var st pushStateJSON
		if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		return st, w.Body.String()
	}

	st, raw := state()
	if len(st.PublicKey) != 87 || !strings.Contains(raw, `"devices":[]`) {
		t.Fatalf("empty state: key len %d, body %s", len(st.PublicKey), raw)
	}

	w := doPush(srv, pushCall{method: "POST", path: "/api/push/devices", origin: "https://my.keenetic.pro", body: body, cookie: cookie})
	if w.Code != http.StatusOK {
		t.Fatalf("POST = %d: %s", w.Code, w.Body.String())
	}
	var dev struct {
		ID        string `json:"id"`
		Label     string `json:"label"`
		CreatedAt int64  `json:"created_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &dev); err != nil {
		t.Fatal(err)
	}
	if dev.ID != "05WsUk2skTnEadK3" || dev.Label != "Chrome, macOS" || dev.CreatedAt <= 0 {
		t.Fatalf("device = %+v", dev)
	}
	if strings.Contains(w.Body.String(), "endpoint") || strings.Contains(w.Body.String(), "p256dh") {
		t.Errorf("POST body leaks subscription: %s", w.Body.String())
	}

	st, raw = state()
	if len(st.Devices) != 1 || st.Devices[0].ID != dev.ID {
		t.Fatalf("devices = %+v", st.Devices)
	}
	var sent struct {
		Keys struct{ P256dh, Auth string } `json:"keys"`
	}
	_ = json.Unmarshal([]byte(body), &sent)
	for _, secret := range []string{"fcm.googleapis.com", "abc123", sent.Keys.P256dh, sent.Keys.Auth, `"endpoint"`, `"p256dh"`, `"auth"`} {
		if strings.Contains(raw, secret) {
			t.Errorf("GET body holds %q: %s", secret, raw)
		}
	}

	for i := 0; i < 2; i++ {
		if w := doPush(srv, pushCall{method: "DELETE", path: "/api/push/devices/" + dev.ID, origin: "https://my.keenetic.pro", cookie: cookie}); w.Code != http.StatusNoContent {
			t.Fatalf("DELETE #%d = %d: %s", i, w.Code, w.Body.String())
		}
	}
	if st, raw = state(); len(st.Devices) != 0 || !strings.Contains(raw, `"devices":[]`) {
		t.Errorf("after delete: %s", raw)
	}
}

func TestPushAPIRejects(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true, AllowedHosts: []string{"my.keenetic.pro"}})
	cookie := loginCookie(t, srv)
	post := func(origin, body string) *httptest.ResponseRecorder {
		return doPush(srv, pushCall{method: "POST", path: "/api/push/devices", origin: origin, body: body, cookie: cookie})
	}
	const https = "https://my.keenetic.pro"
	if w := doPush(srv, pushCall{method: "GET", path: "/api/push", cookie: cookie}); w.Code != http.StatusOK {
		t.Fatalf("GET = %d", w.Code)
	}

	w := doPush(srv, pushCall{method: "POST", path: "/api/push/devices", origin: "http://192.168.1.5:6410", host: "192.168.1.5:6410", body: pushBody(t, "https://push.example/a"), cookie: cookie})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "HTTPS address") {
		t.Errorf("http origin = %d: %s", w.Code, w.Body.String())
	}
	if w := post(https, `{"endpoint":`); w.Code != http.StatusBadRequest {
		t.Errorf("bad JSON = %d, want 400", w.Code)
	}
	if w := post(https, `{"endpoint":"http://push.example/x","keys":{"p256dh":"a","auth":"b"}}`); w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "push.example") {
		t.Errorf("invalid subscription = %d: %s", w.Code, w.Body.String())
	}
	if w := post("https://evil.example", pushBody(t, "https://push.example/a")); w.Code != http.StatusForbidden {
		t.Errorf("foreign origin = %d, want 403", w.Code)
	}
	if w := doPush(srv, pushCall{method: "GET", path: "/api/push"}); w.Code != http.StatusUnauthorized {
		t.Errorf("GET without session = %d, want 401", w.Code)
	}

	for i := 0; i < webpush.MaxDevices; i++ {
		if w := post(https, pushBody(t, fmt.Sprintf("https://push.example/%d", i))); w.Code != http.StatusOK {
			t.Fatalf("device %d = %d: %s", i, w.Code, w.Body.String())
		}
	}
	w = post(https, pushBody(t, "https://push.example/over"))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "device limit reached") {
		t.Errorf("21st device = %d: %s", w.Code, w.Body.String())
	}
	if w := post(https, pushBody(t, "https://push.example/3")); w.Code != http.StatusOK {
		t.Errorf("re-subscribe at the cap = %d, want 200", w.Code)
	}
}

func TestPushSubscribeRefererFallback(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	cookie := loginCookie(t, srv)
	if w := doPush(srv, pushCall{method: "GET", path: "/api/push", cookie: cookie}); w.Code != http.StatusOK {
		t.Fatalf("GET = %d", w.Code)
	}
	req := httptest.NewRequest("POST", "/api/push/devices", strings.NewReader(pushBody(t, "https://push.example/ref")))
	req.Header.Set("X-Ripperr", "1")
	req.RemoteAddr = "192.168.1.10:1234"
	req.Host = "192.168.1.5:6410"
	req.Header.Set("Referer", "https://192.168.1.5:6410/#integrations")
	req.AddCookie(cookie)
	w := httptest.NewRecorder()
	srv.srv.Handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("referer fallback = %d: %s", w.Code, w.Body.String())
	}
	_, devs, err := webpush.NewStore(srv.db).Load(context.Background())
	if err != nil || len(devs) != 1 || devs[0].Origin != "https://192.168.1.5:6410" {
		t.Errorf("stored origin: %+v, err %v", devs, err)
	}
}

func TestPostIntegrationsTestNotifyBrowserChannel(t *testing.T) {
	origSend := sendNotification
	t.Cleanup(func() { sendNotification = origSend })
	var sentCfg notify.Config
	var sentPush *webpush.Store
	sendNotification = func(_ context.Context, cfg notify.Config, _ string, push *webpush.Store, _ notify.Event) error {
		sentCfg, sentPush = cfg, push
		return nil
	}
	origResolver := telegramChatIDResolver
	t.Cleanup(func() { telegramChatIDResolver = origResolver })
	telegramChatIDResolver = func(context.Context, string, string) (string, error) { return "1", nil }

	srv := newTestServer(&fakeStore{})
	post := func(body string) *httptest.ResponseRecorder {
		sentCfg, sentPush = notify.Config{}, nil
		req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", strings.NewReader(body))
		w := httptest.NewRecorder()
		srv.handleTestIntegration(w, req)
		return w
	}
	const creds = `"telegram":{"bot_token":"tok"},"webhook":{"url":"http://hook"},"email":{"host":"smtp.example.com","port":465,"security":"tls","username":"u","password":"p","from":"f@x.example","to":"t@x.example"},"pushover":{"app_token":"a","user_key":"k"},"enabled":true`

	if w := post(`{"kind":"notify","config":{"channel":"browser",` + creds + `}}`); w.Code != http.StatusOK || sentPush == nil {
		t.Fatalf("browser: status %d, push %v, body %s", w.Code, sentPush, w.Body.String())
	}
	if sentCfg.TelegramToken != "" || sentCfg.WebhookURL != "" || sentCfg.EmailHost != "" || sentCfg.EmailPassword != "" || sentCfg.PushoverToken != "" || sentCfg.PushoverUser != "" {
		t.Errorf("browser channel keeps creds: %+v", sentCfg)
	}
	if w := post(`{"kind":"notify","config":{"channel":"telegram",` + creds + `}}`); w.Code != http.StatusOK || sentPush != nil {
		t.Errorf("telegram: status %d, push %v, want nil", w.Code, sentPush)
	}
	if w := post(`{"kind":"notify","config":{` + creds + `}}`); w.Code != http.StatusOK || sentPush == nil {
		t.Errorf("no channel: status %d, push %v, want set", w.Code, sentPush)
	}
	w := post(`{"kind":"notify","config":{"channel":"sms","enabled":true}}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "channel must be telegram, webhook, email, pushover, or browser") {
		t.Errorf("sms: status %d, body %s", w.Code, w.Body.String())
	}
}

func TestPushSubscribeBodyOriginBehindProxy(t *testing.T) {
	srv := newConfiguredAuthServer(t, config.Web{Listen: ":0", Enabled: true})
	cookie := loginCookie(t, srv)
	if w := doPush(srv, pushCall{method: "GET", path: "/api/push", cookie: cookie}); w.Code != http.StatusOK {
		t.Fatalf("GET = %d", w.Code)
	}
	post := func(origin string) *httptest.ResponseRecorder {
		var body map[string]any
		if err := json.Unmarshal([]byte(pushBody(t, "https://push.example/proxy")), &body); err != nil {
			t.Fatal(err)
		}
		body["origin"] = origin
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", "/api/push/devices", strings.NewReader(string(raw)))
		req.Header.Set("X-Ripperr", "1")
		req.RemoteAddr = "192.168.1.10:1234"
		req.Host = "192.168.1.5:6410"
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		srv.srv.Handler.ServeHTTP(w, req)
		return w
	}
	if w := post("http://track.example:8443"); w.Code != http.StatusBadRequest {
		t.Fatalf("http body origin = %d, want 400", w.Code)
	}
	if w := post("https://track.example:8443"); w.Code != http.StatusOK {
		t.Fatalf("https body origin without Origin and Referer = %d: %s", w.Code, w.Body.String())
	}
	_, devs, err := webpush.NewStore(srv.db).Load(context.Background())
	if err != nil || len(devs) != 1 || devs[0].Origin != "https://track.example:8443" {
		t.Errorf("stored origin: %+v, err %v", devs, err)
	}
}
