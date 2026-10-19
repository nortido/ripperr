package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ripperr/internal/config"
	"ripperr/internal/runner"
)

type probeCall struct {
	n   int
	cfg config.App
}

func stubMirrorProbe(t *testing.T, hosts int, err error) *probeCall {
	t.Helper()
	call := &probeCall{}
	orig := mirrorSourceProbe
	mirrorSourceProbe = func(_ context.Context, cfg config.App) (int, error) {
		call.n++
		call.cfg = cfg
		return hosts, err
	}
	t.Cleanup(func() { mirrorSourceProbe = orig })
	return call
}

func mirrorTestServer(eff config.App) *Server {
	return withTestSession(New(&fakeStore{}, func() {}, func() config.App { return eff }, ":0", runner.NewState(), nil))
}

func postIntegrationTest(srv *Server, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	srv.handleTestIntegration(w, httptest.NewRequest(http.MethodPost, "/api/integrations/test", strings.NewReader(body)))
	return w
}

func TestMirrorSourceTestUsesFormValues(t *testing.T) {
	call := stubMirrorProbe(t, 3, nil)
	eff := testEffective()
	eff.Runner.DownloadProxy = "http://proxy.example:3128"
	w := postIntegrationTest(mirrorTestServer(eff), `{"kind":"mirror_source","config":{"type":"custom","url":"https://m.example/{path}","direct":true}}`)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"ok":true,"hosts":3}` {
		t.Fatalf("test = %d %s, want 200 {\"ok\":true,\"hosts\":3}", w.Code, w.Body.String())
	}
	want := config.MirrorSource{Type: "custom", URL: "https://m.example/{path}", Direct: true}
	if call.n != 1 || call.cfg.MirrorSource != want {
		t.Errorf("probe calls=%d source=%+v, want 1 call with %+v", call.n, call.cfg.MirrorSource, want)
	}
	if call.cfg.Runner.DownloadProxy != "http://proxy.example:3128" {
		t.Errorf("probe proxy = %q, want the saved download proxy", call.cfg.Runner.DownloadProxy)
	}
}

func TestMirrorSourceTestRejectsInvalidForm(t *testing.T) {
	for name, body := range map[string]string{
		"http scheme":  `{"kind":"mirror_source","config":{"type":"custom","url":"http://m.example/{path}"}}`,
		"empty type":   `{"kind":"mirror_source","config":{"type":""}}`,
		"unknown type": `{"kind":"mirror_source","config":{"type":"ftp"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			call := stubMirrorProbe(t, 1, nil)
			w := postIntegrationTest(mirrorTestServer(testEffective()), body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("code = %d %s, want 400", w.Code, w.Body.String())
			}
			if call.n != 0 {
				t.Error("probe called for an invalid form")
			}
		})
	}
}

func TestMirrorSourceTestKeepsDirectWhenOmitted(t *testing.T) {
	call := stubMirrorProbe(t, 1, nil)
	eff := testEffective()
	eff.MirrorSource = config.MirrorSource{Type: "github", Direct: true}
	postIntegrationTest(mirrorTestServer(eff), `{"kind":"mirror_source","config":{"type":"jsdelivr"}}`)
	want := config.MirrorSource{Type: "jsdelivr", Direct: true}
	if call.cfg.MirrorSource != want {
		t.Errorf("source = %+v, want %+v", call.cfg.MirrorSource, want)
	}
}

func TestMirrorSourceTestNullConfigUsesEffective(t *testing.T) {
	call := stubMirrorProbe(t, 1, nil)
	eff := testEffective()
	eff.MirrorSource = config.MirrorSource{Type: "custom", URL: "https://saved.example/{path}"}
	postIntegrationTest(mirrorTestServer(eff), `{"kind":"mirror_source","config":null}`)
	if call.n != 1 || call.cfg.MirrorSource != eff.MirrorSource {
		t.Errorf("calls=%d source=%+v, want the effective %+v", call.n, call.cfg.MirrorSource, eff.MirrorSource)
	}
}

func TestMirrorSourceTestFailureLogged(t *testing.T) {
	stubMirrorProbe(t, 0, errors.New("boom"))
	before := loggedCount("integration test failed: mirror_source: boom")
	w := postIntegrationTest(mirrorTestServer(testEffective()), `{"kind":"mirror_source","config":{"type":"github"}}`)
	if strings.TrimSpace(w.Body.String()) != `{"ok":false,"error":"boom"}` {
		t.Errorf("body = %s, want {\"ok\":false,\"error\":\"boom\"}", w.Body.String())
	}
	if got := loggedCount("integration test failed: mirror_source: boom") - before; got != 1 {
		t.Errorf("WARN lines = %d, want 1", got)
	}
}

func TestIntegrationTestKindTexts(t *testing.T) {
	w := postIntegrationTest(mirrorTestServer(testEffective()), `{"kind":"nope"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "mirror_source") {
		t.Errorf("unknown kind = %d %s, want 400 listing mirror_source", w.Code, w.Body.String())
	}
	w = postIntegrationTest(mirrorTestServer(testEffective()), `{"kind":"nope","config":{}}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "mirror_source") {
		t.Errorf("unknown kind with config = %d %s, want 400 listing mirror_source", w.Code, w.Body.String())
	}
}

func TestProxyTestBodyHasNoHosts(t *testing.T) {
	w := postIntegrationTest(mirrorTestServer(testEffective()), `{"kind":"proxy"}`)
	if strings.Contains(w.Body.String(), "hosts") {
		t.Errorf("proxy body = %s, want no hosts key", w.Body.String())
	}
}
