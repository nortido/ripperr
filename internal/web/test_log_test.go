package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ripperr/internal/system/logger"
)

func loggedWith(substr string) bool {
	return loggedCount(substr) > 0
}

func loggedCount(substr string) int {
	n := 0
	for _, e := range logger.Snapshot(0) {
		if strings.Contains(e.Message, substr) {
			n++
		}
	}
	return n
}

func TestFailedTrackerTestLogged(t *testing.T) {
	srv := newAddedServer(&fakeStore{settings: map[string]json.RawMessage{}}, new(int))
	w := serveAdded(srv, http.MethodPost, "/api/trackers/rutracker/test", `{"config":{"hour_limit":"bad"}}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("test = %d %s, want 200 ok false", w.Code, w.Body.String())
	}
	if !loggedWith("tracker test failed: rutracker: ") {
		t.Error("failed tracker test is not in the log")
	}
}

func TestFailedIntegrationTestLogged(t *testing.T) {
	srv := newTestServer(&fakeStore{})
	w := httptest.NewRecorder()
	srv.handleTestIntegration(w, httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader([]byte(`{"kind":"notify"}`))))
	if !strings.Contains(w.Body.String(), `"ok":false`) {
		t.Fatalf("test = %s, want ok false", w.Body.String())
	}
	if !loggedWith("integration test failed: notify: ") {
		t.Error("failed integration test is not in the log")
	}
}

func TestStoppedIntegrationTestNotLogged(t *testing.T) {
	// буфер лога общий, другие тесты уже писали сбои proxy
	before := loggedCount("integration test failed: proxy: ")
	srv := newTestServer(&fakeStore{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodPost, "/api/integrations/test", bytes.NewReader([]byte(`{"kind":"proxy"}`))).WithContext(ctx)
	srv.handleTestIntegration(httptest.NewRecorder(), req)
	if loggedCount("integration test failed: proxy: ") != before {
		t.Error("test stopped by the user is logged as a failure")
	}
}
