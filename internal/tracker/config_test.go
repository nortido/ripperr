package tracker

import (
	"context"
	"encoding/json"
	"testing"

	"ripperr/internal/config"
	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/settings"
	"ripperr/internal/tracker/registry"
)

type configSettingsDB struct {
	data map[string]json.RawMessage
}

func (f *configSettingsDB) GetSetting(_ context.Context, key string) (json.RawMessage, bool, error) {
	v, ok := f.data[key]
	return v, ok, nil
}

func (f *configSettingsDB) SaveSetting(_ context.Context, key string, value json.RawMessage) error {
	if f.data == nil {
		f.data = make(map[string]json.RawMessage)
	}
	f.data[key] = value
	return nil
}

func rutrackerCredsFromOverlay(t *testing.T, overlay string) string {
	t.Helper()
	cfg := config.App{Tracker: registry.ConfigDefaults()}
	db := &configSettingsDB{data: map[string]json.RawMessage{"trackers": json.RawMessage(overlay)}}
	if err := settings.ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	return ConfigFromApp(cfg)["rutracker"].Credentials
}

func TestConfigFromAppExpandsLoginPlaceholders(t *testing.T) {
	got := rutrackerCredsFromOverlay(t, `{"rutracker":{"credentials":"login_username={login}&login_password={password}&login=%D0%B2%D1%85%D0%BE%D0%B4","login":"va&sya {password}","password":"p+w%{}="}}`)
	want := "login_username=va%26sya+%7Bpassword%7D&login_password=p%2Bw%25%7B%7D%3D&login=%D0%B2%D1%85%D0%BE%D0%B4"
	if got != want {
		t.Fatalf("credentials = %q, want %q", got, want)
	}

	other := rutrackerCredsFromOverlay(t, `{"rutracker":{"credentials":"login_username={login}&login_password={password}&login=%D0%B2%D1%85%D0%BE%D0%B4","login":"va&sya {password}","password":"SEC-other"}}`)
	if other == got {
		t.Fatalf("password change kept body %q", other)
	}

	raw := "login_username=vasya&login_password=SEC-raw"
	if got := rutrackerCredsFromOverlay(t, `{"rutracker":{"credentials":"`+raw+`","login":"x","password":"SEC-y"}}`); got != raw {
		t.Fatalf("body without placeholders = %q, want %q", got, raw)
	}
	if got := rutrackerCredsFromOverlay(t, `{"rutracker":{"credentials":"","login":"x","password":"SEC-y"}}`); got != "" {
		t.Fatalf("empty body = %q", got)
	}
}

func TestTestLoginAttrsExpandsPlaceholders(t *testing.T) {
	cfg := config.App{Tracker: config.Tracker{"kinozal": {
		Credentials: "username={login}&password={password}&to=%2F",
		Login:       "va sya",
		Password:    "SEC-p&q",
	}}}
	attrs := testLoginAttrs(cfg, "kinozal", 2, nil)
	if want := "username=va+sya&password=SEC-p%26q&to=%2F"; attrs.Credentials != want {
		t.Fatalf("credentials = %q, want %q", attrs.Credentials, want)
	}
}

func TestTrackerFromItemFlareUsesChromeFingerprint(t *testing.T) {
	for in, want := range map[int]proxyModel.ProxyType{0: proxyModel.TypeNone, 1: proxyModel.TypeTLS, 2: proxyModel.TypeTLS} {
		if got := trackerFromItem(config.TrackerItem{ProxyType: in}).ProxyType; got != want {
			t.Errorf("proxy_type %d: got %d, want %d", in, got, want)
		}
	}
}
