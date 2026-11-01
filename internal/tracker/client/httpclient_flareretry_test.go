package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"ripperr/internal/helper"
	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/model/tracker"
	"ripperr/internal/proxy"
)

const challengeSolveFailureMessage = "Error: Error solving the challenge. Message: invalid cookie domain (Session info: chrome=137.0.7151.103)"

func TestIsFlareChallengeSolveFailureRecognizesIncidentMessage(t *testing.T) {
	if !isFlareChallengeSolveFailure(errors.New(challengeSolveFailureMessage)) {
		t.Fatal("expected the incident's invalid cookie domain message to be recognized")
	}
}

func TestIsFlareChallengeSolveFailureIgnoresTimeout(t *testing.T) {
	if isFlareChallengeSolveFailure(errors.New("Timeout after 60000ms")) {
		t.Fatal("a solve timeout must not be treated as a challenge solve failure")
	}
}

func TestIsFlareChallengeSolveFailureIgnoresUnrelatedError(t *testing.T) {
	if isFlareChallengeSolveFailure(errors.New("session not found")) {
		t.Fatal("unrelated flaresolverr error must not be recognized")
	}
}

func TestIsFlareChallengeSolveFailureHandlesNil(t *testing.T) {
	if isFlareChallengeSolveFailure(nil) {
		t.Fatal("nil error must not be recognized")
	}
}

func newFlareStubServer(t *testing.T, requestFailures int, destroyCalls *int32, requestCalls *int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Cmd string `json:"cmd"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Cmd == "sessions.destroy" {
			atomic.AddInt32(destroyCalls, 1)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		n := atomic.AddInt32(requestCalls, 1)
		if int(n) <= requestFailures {
			_, _ = w.Write([]byte(`{"status":"error","message":"` + challengeSolveFailureMessage + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","solution":{"status":200,"response":"<html>ok</html>","cookies":[],"userAgent":"test-agent"}}`))
	}))
}

func newFlareClient(t *testing.T, serverURL, session string) *client {
	t.Helper()
	tr := &flareOptsTracker{Tracker: tracker.Tracker{ProxyType: proxyModel.TypeFlare, Name: "test"}, session: session}
	return &client{
		ITracker: tr,
		header:   &http.Header{},
		cb:       helper.NewCircuitBreaker(&helper.CircuitBreakerConfig{}),
		px:       proxy.New(&http.Client{}, &proxyModel.Config{Flaresolverr: proxyModel.ItemConfig{URL: serverURL, MaxTimeout: 1000}}),
	}
}

func TestSendViaFlareRetriesOnceAfterChallengeFailureAndSucceeds(t *testing.T) {
	var destroyCalls, requestCalls int32
	server := newFlareStubServer(t, 1, &destroyCalls, &requestCalls)
	defer server.Close()

	c := newFlareClient(t, server.URL, "session-1")
	res, err := c.GetResponse(context.Background(), "https://example.com/topic/1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected response")
	}
	if requestCalls != 2 {
		t.Fatalf("request calls = %d, want 2 (1 failure + 1 retry)", requestCalls)
	}
	if destroyCalls != 1 {
		t.Fatalf("session destroy calls = %d, want 1", destroyCalls)
	}
}

func TestSendViaFlareTwoFailuresReturnsErrorAfterExactlyOneRetry(t *testing.T) {
	var destroyCalls, requestCalls int32
	server := newFlareStubServer(t, 2, &destroyCalls, &requestCalls)
	defer server.Close()

	c := newFlareClient(t, server.URL, "session-1")
	_, err := c.GetResponse(context.Background(), "https://example.com/topic/1", false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalid cookie domain") {
		t.Fatalf("err = %v, want challenge solve failure surfaced", err)
	}
	if requestCalls != 2 {
		t.Fatalf("request calls = %d, want exactly 2, no looping", requestCalls)
	}
	if destroyCalls != 1 {
		t.Fatalf("session destroy calls = %d, want 1", destroyCalls)
	}
}

func TestSendViaFlareNoSessionSkipsRetry(t *testing.T) {
	var destroyCalls, requestCalls int32
	server := newFlareStubServer(t, 1, &destroyCalls, &requestCalls)
	defer server.Close()

	c := newFlareClient(t, server.URL, "")
	_, err := c.GetResponse(context.Background(), "https://example.com/topic/1", false)
	if err == nil {
		t.Fatal("expected error")
	}
	if requestCalls != 1 {
		t.Fatalf("request calls = %d, want 1 (no retry without a session)", requestCalls)
	}
	if destroyCalls != 0 {
		t.Fatalf("session destroy calls = %d, want 0", destroyCalls)
	}
}

func TestSendViaFlareSuccessDoesNotRetry(t *testing.T) {
	var destroyCalls, requestCalls int32
	server := newFlareStubServer(t, 0, &destroyCalls, &requestCalls)
	defer server.Close()

	c := newFlareClient(t, server.URL, "session-1")
	if _, err := c.GetResponse(context.Background(), "https://example.com/topic/1", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if requestCalls != 1 {
		t.Fatalf("request calls = %d, want 1", requestCalls)
	}
	if destroyCalls != 0 {
		t.Fatalf("session destroy calls = %d, want 0", destroyCalls)
	}
}

func TestSendViaFlareUnrelatedErrorDoesNotRetry(t *testing.T) {
	// без счетчиков тест прошел бы, даже если повтор начнут делать на любую ошибку
	var destroyCalls, requestCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Cmd string `json:"cmd"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Cmd == "sessions.destroy" {
			atomic.AddInt32(&destroyCalls, 1)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		atomic.AddInt32(&requestCalls, 1)
		_, _ = w.Write([]byte(`{"status":"error","message":"session not found"}`))
	}))
	defer server.Close()

	c := newFlareClient(t, server.URL, "session-1")
	_, err := c.GetResponse(context.Background(), "https://example.com/topic/1", false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("err = %v, want unrelated flaresolverr error surfaced", err)
	}
	if requestCalls != 1 {
		t.Fatalf("request calls = %d, want 1 (no retry on unrelated error)", requestCalls)
	}
	if destroyCalls != 0 {
		t.Fatalf("session destroy calls = %d, want 0", destroyCalls)
	}
}

func TestSendViaFlareTimeoutDoesNotRetry(t *testing.T) {
	var destroyCalls, requestCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Cmd string `json:"cmd"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Cmd == "sessions.destroy" {
			atomic.AddInt32(&destroyCalls, 1)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		atomic.AddInt32(&requestCalls, 1)
		_, _ = w.Write([]byte(`{"status":"error","message":"Timeout after 60000ms"}`))
	}))
	defer server.Close()

	c := newFlareClient(t, server.URL, "session-1")
	_, err := c.GetResponse(context.Background(), "https://example.com/topic/1", false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Timeout after") {
		t.Fatalf("err = %v, want timeout error surfaced as is", err)
	}
	if requestCalls != 1 {
		t.Fatalf("request calls = %d, want 1 (no retry on a solve timeout)", requestCalls)
	}
	if destroyCalls != 0 {
		t.Fatalf("session destroy calls = %d, want 0 (session must survive a timeout)", destroyCalls)
	}
}

func TestSendViaFlareRespectsCanceledContext(t *testing.T) {
	var destroyCalls, requestCalls int32
	server := newFlareStubServer(t, 1, &destroyCalls, &requestCalls)
	defer server.Close()

	c := newFlareClient(t, server.URL, "session-1")
	// doHttp зовет px.Send через обычный http.Client, а тот ctx.Err() на входе не
	// смотрит. Поэтому дергаем sendViaFlare напрямую с уже отмененым контекстом
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com/topic/1", nil)
	if err != nil {
		t.Fatal(err)
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.sendViaFlare(canceledCtx, req, c.flareOptions(nil, false), proxyModel.TypeFlare)
	if err == nil {
		t.Fatal("expected error")
	}
	if requestCalls != 1 {
		t.Fatalf("request calls = %d, want 1 (canceled context must not trigger a retry)", requestCalls)
	}
	if destroyCalls != 0 {
		t.Fatalf("session destroy calls = %d, want 0", destroyCalls)
	}
}

func newClearanceRejectingServer(t *testing.T, payloads *[][]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Cmd     string `json:"cmd"`
			Cookies []struct {
				Name string `json:"name"`
			} `json:"cookies"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Cmd == "sessions.destroy" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		names := make([]string, 0, len(body.Cookies))
		for _, c := range body.Cookies {
			names = append(names, c.Name)
		}
		*payloads = append(*payloads, names)
		if slices.Contains(names, "cf_clearance") {
			_, _ = w.Write([]byte(`{"status":"error","message":"any solver wording"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok","solution":{"status":200,"response":"<html>ok</html>","cookies":[{"name":"cf_clearance","value":"fresh"}],"userAgent":"test-agent"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSendViaFlareDropsRejectedClearanceAndRetries(t *testing.T) {
	var payloads [][]string
	c := newFlareClient(t, newClearanceRejectingServer(t, &payloads).URL, "")
	c.setCookies([]*http.Cookie{{Name: "bb_session", Value: "s"}, {Name: "cf_clearance", Value: "stale"}})

	res, err := c.GetResponse(context.Background(), "https://example.com/topic/1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = res.Body.Close()
	if len(payloads) != 2 {
		t.Fatalf("solver calls = %d, want 2", len(payloads))
	}
	if !slices.Equal(payloads[1], []string{"bb_session"}) {
		t.Fatalf("retry cookies = %v, want only bb_session", payloads[1])
	}
	for _, ck := range c.cookieSnapshot() {
		if ck.Name == "cf_clearance" && ck.Value != "fresh" {
			t.Fatalf("cf_clearance = %q, want the fresh one", ck.Value)
		}
	}
}

func TestSendViaFlareWithoutClearanceDoesNotRetry(t *testing.T) {
	var destroyCalls, requestCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&requestCalls, 1)
		_, _ = w.Write([]byte(`{"status":"error","message":"any solver wording"}`))
	}))
	defer server.Close()
	c := newFlareClient(t, server.URL, "session-1")
	c.setCookies([]*http.Cookie{{Name: "bb_session", Value: "s"}})

	if _, err := c.GetResponse(context.Background(), "https://example.com/topic/1", false); err == nil {
		t.Fatal("expected error")
	}
	if requestCalls != 1 || destroyCalls != 0 {
		t.Fatalf("calls = %d, destroys = %d, want one call and no reset", requestCalls, destroyCalls)
	}
}
