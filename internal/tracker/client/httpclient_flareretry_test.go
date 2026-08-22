package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"trackerr/internal/helper"
	proxyModel "trackerr/internal/model/proxy"
	"trackerr/internal/model/tracker"
	"trackerr/internal/proxy"
)

const challengeSolveFailureMessage = "Error: Error solving the challenge. Message: invalid cookie domain (Session info: chrome=137.0.7151.103)"

func TestIsFlareChallengeSolveFailureRecognizesIncidentMessage(t *testing.T) {
	if !isFlareChallengeSolveFailure(errors.New(challengeSolveFailureMessage)) {
		t.Fatal("expected the incident's invalid cookie domain message to be recognized")
	}
}

func TestIsFlareChallengeSolveFailureIgnoresTimeout(t *testing.T) {
	// таймаут разгадывания это отдельный класс сбоя (см. helper.IsFlareTimeout),
	// узкий предикат под него срабатывать не должен
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
	// первый запрос падает на "invalid cookie domain", сессия сбрасывается,
	// повтор поднимает чистый браузер и получает ответ без вмешательства
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
	// повтор ровно один: второй такой же отказ отдаётся вызывающему как есть,
	// без циклов
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
	// сессии не было вовсе: повторять с чистого листа нечего, ошибка уходит сразу
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
	// успешный ответ FlareSolverr не должен тянуть за собой лишние запросы
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
	// чужая ошибка FlareSolverr (не про неудачу разгадывания челленджа) не
	// должна вызывать самолечение сессии: без counters тест зеленел бы даже
	// если бы условие повтора выродилось в "повторять при любой ошибке"
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
	// таймаут разгадывания это не тот же класс сбоя, что invalid cookie
	// domain: у него своя семантика через shouldReloginAfterFlare, и вешать
	// на него повтор с уничтожением сессии удвоило бы стоимость каждого
	// таймаута и урезало бы окно circuit breaker вдвое
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
	// контекст вызывающего уважается: если он уже отменён, повтор не делается
	var destroyCalls, requestCalls int32
	server := newFlareStubServer(t, 1, &destroyCalls, &requestCalls)
	defer server.Close()

	c := newFlareClient(t, server.URL, "session-1")
	// ponytail: doHttp зовёт px.Send синхронно поверх обычного http.Client,
	// который сам ctx.Err() на входе не проверяет - дергаем sendViaFlare
	// напрямую с уже отменённым контекстом, минуя реальную сетевую отмену
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
