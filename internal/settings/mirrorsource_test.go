package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/storage/client/sqlite"
)

func TestValidateMirrorSourceURL(t *testing.T) {
	for _, raw := range []string{
		"https://mirror.example.com/jackett/{path}",
		"https://gitflic.ru/project/u/jackett/blob/raw?file={path}&branch=master",
		" https://mirror.example.com/{path} ",
		"https://m.example/x?file={path}#top",
	} {
		if err := ValidateMirrorSourceURL(raw); err != nil {
			t.Errorf("%q: unexpected error: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"",
		"   ",
		"http://m.example/{path}",
		"https://m.example/",
		"https://m.example/{path}/{path}",
		"https://{path}/x",
		"https://m.example{path}",
		"https://m.example/#{path}",
		"https://m.example/x#a{path}",
		"https://u:pw@m.example/{path}",
		"https://localhost/{path}",
		"https://127.0.0.1/{path}",
		"https://[::1]/{path}",
		"https://0.0.0.0/{path}",
		"https://192.168.1.2/{path}",
		"https://nas/{path}",
		"https://box.local/{path}",
		"https://BOX.LOCAL./{path}",
		"https://localhost./{path}",
	} {
		err := ValidateMirrorSourceURL(raw)
		if err == nil {
			t.Errorf("%q: expected error", raw)
			continue
		}
		if strings.Contains(err.Error(), "pw") {
			t.Errorf("%q: error leaks the password: %v", raw, err)
		}
	}
}

func TestValidateMirrorSource(t *testing.T) {
	for _, ms := range []config.MirrorSource{
		{},
		{Type: "github", URL: "garbage"},
		{Type: "jsdelivr", URL: "http://x"},
		{Type: "custom", URL: "https://m.example/{path}"},
	} {
		if err := ValidateMirrorSource(ms); err != nil {
			t.Errorf("%+v: unexpected error: %v", ms, err)
		}
	}
	for _, ms := range []config.MirrorSource{
		{Type: "custom"},
		{Type: "custom", URL: "http://m.example/{path}"},
		{Type: "bogus"},
	} {
		if err := ValidateMirrorSource(ms); err == nil {
			t.Errorf("%+v: expected error", ms)
		}
	}
}

func mirrorSourceOf(t *testing.T, db *fakeSettingsDB) config.MirrorSource {
	t.Helper()
	cfg := config.App{MirrorSource: config.MirrorSource{Type: "github"}}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	return cfg.MirrorSource
}

func TestMergeIntegrationsMirrorSource(t *testing.T) {
	ctx := context.Background()
	db := &fakeSettingsDB{}
	custom := config.MirrorSource{Type: "custom", URL: "https://m.example/{path}", Direct: true}
	if err := MergeIntegrations(ctx, db, json.RawMessage(`{"mirror_source":{"type":"custom","url":" https://m.example/{path} ","direct":true}}`)); err != nil {
		t.Fatal(err)
	}
	if got := mirrorSourceOf(t, db); got != custom {
		t.Fatalf("after custom save = %+v, want %+v", got, custom)
	}

	// скрытый адрес при github/jsdelivr не блокирует сохранение
	if err := MergeIntegrations(ctx, db, json.RawMessage(`{"mirror_source":{"type":"jsdelivr","url":"bad","direct":false}}`)); err != nil {
		t.Fatal(err)
	}
	want := config.MirrorSource{Type: "jsdelivr", URL: custom.URL}
	if got := mirrorSourceOf(t, db); got != want {
		t.Fatalf("after jsdelivr save = %+v, want %+v", got, want)
	}

	before := bytes.Clone(db.data[sqlite.SettingIntegrations])
	for _, patch := range []string{
		`{"mirror_source":{"type":"custom"}}`,
		`{"mirror_source":{"type":"custom","url":"  "}}`,
		`{"mirror_source":{"type":"custom","url":"http://m.example/{path}"}}`,
		`{"mirror_source":{"type":""}}`,
		`{"mirror_source":{"type":"bogus"}}`,
		`{"mirror_source":{"url":"bad"}}`,
	} {
		if err := MergeIntegrations(ctx, db, json.RawMessage(patch)); err == nil {
			t.Errorf("%s: expected error", patch)
		}
	}
	if !bytes.Equal(db.data[sqlite.SettingIntegrations], before) {
		t.Fatalf("rejected patches changed the doc: %s", db.data[sqlite.SettingIntegrations])
	}

	if err := MergeIntegrations(ctx, db, json.RawMessage(`{"mirror_source":{"url":"https://other.example/x/{path}"}}`)); err != nil {
		t.Fatal(err)
	}
	want.URL = "https://other.example/x/{path}"
	if got := mirrorSourceOf(t, db); got != want {
		t.Fatalf("after url-only save = %+v, want %+v", got, want)
	}

	if err := MergeIntegrations(ctx, db, json.RawMessage(`{"qbit":{"host":"new"}}`)); err != nil {
		t.Fatal(err)
	}
	if got := mirrorSourceOf(t, db); got != want {
		t.Fatalf("after unrelated save = %+v, want %+v", got, want)
	}
}

func TestApplyOverridesWithoutMirrorSourceKeepsEnv(t *testing.T) {
	db := &fakeSettingsDB{data: map[string]json.RawMessage{
		sqlite.SettingIntegrations: json.RawMessage(`{"qbit":{"host":"http://q"},"mirror_source":{}}`),
	}}
	env := config.MirrorSource{Type: "custom", URL: "https://m.example/{path}", Direct: true}
	cfg := config.App{MirrorSource: env}
	if err := ApplyOverrides(context.Background(), &cfg, db); err != nil {
		t.Fatal(err)
	}
	if cfg.MirrorSource != env {
		t.Fatalf("MirrorSource = %+v, want env %+v", cfg.MirrorSource, env)
	}
}

func TestUnmaskIntegrationsPatchKeepsMirrorSource(t *testing.T) {
	raw := json.RawMessage(`{"mirror_source":{"type":"custom","url":"https://m.example/{path}","direct":true}}`)
	out, err := UnmaskIntegrationsPatch(raw, &config.App{})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		MirrorSource map[string]any `json:"mirror_source"`
	}
	if err = json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.MirrorSource["type"] != "custom" || got.MirrorSource["url"] != "https://m.example/{path}" || got.MirrorSource["direct"] != true {
		t.Fatalf("mirror_source = %v, want the section kept: %s", got.MirrorSource, out)
	}
}

func TestIntegrationsJSONMirrorSource(t *testing.T) {
	type section struct {
		MirrorSource *struct {
			Type   string `json:"type"`
			URL    string `json:"url"`
			Direct bool   `json:"direct"`
		} `json:"mirror_source"`
	}
	var s section
	if err := json.Unmarshal(IntegrationsJSON(&config.App{}), &s); err != nil {
		t.Fatal(err)
	}
	if s.MirrorSource == nil || s.MirrorSource.Type != "github" || s.MirrorSource.URL != "" || s.MirrorSource.Direct {
		t.Fatalf("empty type: mirror_source = %+v, want github", s.MirrorSource)
	}
	cfg := config.App{MirrorSource: config.MirrorSource{Type: "custom", URL: "https://m.example/{path}", Direct: true}}
	s = section{}
	if err := json.Unmarshal(IntegrationsJSON(&cfg), &s); err != nil {
		t.Fatal(err)
	}
	if s.MirrorSource == nil || s.MirrorSource.Type != "custom" || s.MirrorSource.URL != "https://m.example/{path}" || !s.MirrorSource.Direct {
		t.Fatalf("custom: mirror_source = %+v", s.MirrorSource)
	}
}

func TestApplyOverridesTrimsEnvMirrorSourceURL(t *testing.T) {
	cfg := config.App{MirrorSource: config.MirrorSource{Type: "custom", URL: " https://m.example/{path} "}}
	if err := ApplyOverrides(context.Background(), &cfg, &fakeSettingsDB{}); err != nil {
		t.Fatal(err)
	}
	if cfg.MirrorSource.URL != "https://m.example/{path}" {
		t.Fatalf("URL = %q, want trimmed", cfg.MirrorSource.URL)
	}
}
