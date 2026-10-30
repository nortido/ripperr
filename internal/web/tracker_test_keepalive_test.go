package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	trackerPkg "ripperr/internal/tracker"
)

func runRespondTrackerTest(t *testing.T, delay time.Duration, result error) *httptest.ResponseRecorder {
	t.Helper()
	prev := testKeepalive
	testKeepalive = 5 * time.Millisecond
	t.Cleanup(func() { testKeepalive = prev })
	done := make(chan error, 1)
	go func() {
		time.Sleep(delay)
		done <- result
	}()
	rec := httptest.NewRecorder()
	respondTrackerTest(rec, httptest.NewRequest(http.MethodPost, "/api/trackers/x/test", nil), "x", done)
	return rec
}

func TestRespondTrackerTestKeepsSlowTestAlive(t *testing.T) {
	rec := runRespondTrackerTest(t, 60*time.Millisecond, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(body, " ") || strings.TrimSpace(body) != `{"ok":true}` {
		t.Fatalf("code=%d body=%q", rec.Code, body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type %q", got)
	}
}

func TestRespondTrackerTestLateErrors(t *testing.T) {
	rec := runRespondTrackerTest(t, 60*time.Millisecond, trackerPkg.ErrUnknownTracker)
	if body := strings.TrimSpace(rec.Body.String()); rec.Code != http.StatusOK || !strings.Contains(body, `"ok":false`) || !strings.Contains(body, "unknown tracker key") {
		t.Fatalf("code=%d body=%q", rec.Code, body)
	}
	rec = runRespondTrackerTest(t, 60*time.Millisecond, errors.New("boom"))
	if body := strings.TrimSpace(rec.Body.String()); !strings.Contains(body, `"ok":false`) || !strings.Contains(body, "boom") {
		t.Fatalf("body=%q", body)
	}
}

func TestRespondTrackerTestFastHasNoPadding(t *testing.T) {
	prev := testKeepalive
	testKeepalive = time.Hour
	t.Cleanup(func() { testKeepalive = prev })
	done := make(chan error, 1)
	done <- trackerPkg.ErrUnknownTracker
	rec := httptest.NewRecorder()
	respondTrackerTest(rec, httptest.NewRequest(http.MethodPost, "/api/trackers/x/test", nil), "x", done)
	if rec.Code != http.StatusNotFound || strings.HasPrefix(rec.Body.String(), " ") {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}
