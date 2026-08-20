package helper

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCircuitBreaker_TripsAfterMaxFailures(t *testing.T) {
	cb := NewCircuitBreaker(&CircuitBreakerConfig{MaxFailures: 2, Timeout: time.Hour})
	failing := func() error { return errors.New("boom") }

	if err := cb.Call(failing); err == nil {
		t.Fatal("expected error from failing call")
	}
	if cb.state != StateClosed {
		t.Fatalf("expected still closed after 1 failure, got %v", cb.state)
	}

	if err := cb.Call(failing); err == nil {
		t.Fatal("expected error from failing call")
	}
	if cb.state != StateOpen {
		t.Fatalf("expected open after MaxFailures reached, got %v", cb.state)
	}

	called := false
	err := cb.Call(func() error { called = true; return nil })
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen while blocking, got %v", err)
	}
	if called {
		t.Fatal("fn must not be called while circuit is open")
	}
}

func TestCircuitBreaker_HalfOpenRecovery(t *testing.T) {
	cb := NewCircuitBreaker(&CircuitBreakerConfig{MaxFailures: 1, Timeout: time.Millisecond})
	_ = cb.Call(func() error { return errors.New("boom") })
	if cb.state != StateOpen {
		t.Fatalf("expected open after failure, got %v", cb.state)
	}
	time.Sleep(5 * time.Millisecond)

	// Первый вызов после истечения таймаута переводит Open в HalfOpen и
	// сразу выполняет пробный вызов.
	if err := cb.Call(func() error { return nil }); err != nil {
		t.Fatalf("expected successful half-open transition call, got %v", err)
	}
	if cb.state != StateClosed {
		t.Fatalf("expected closed after successful half-open call, got %v", cb.state)
	}
	if cb.failures != 0 {
		t.Fatalf("expected failures reset, got %d", cb.failures)
	}
}

func TestCircuitBreaker_ResetsFailuresOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker(&CircuitBreakerConfig{MaxFailures: 3, Timeout: time.Hour})
	_ = cb.Call(func() error { return errors.New("boom") })
	if cb.failures != 1 {
		t.Fatalf("expected 1 recorded failure, got %d", cb.failures)
	}

	if err := cb.Call(func() error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cb.failures != 0 {
		t.Fatalf("expected failures reset after success, got %d", cb.failures)
	}
	if cb.state != StateClosed {
		t.Fatalf("expected still closed, got %v", cb.state)
	}
}

func TestCircuitBreaker_ContextCanceledNotAFailure(t *testing.T) {
	cb := NewCircuitBreaker(&CircuitBreakerConfig{MaxFailures: 1, Timeout: time.Hour})
	err := cb.Call(func() error { return context.Canceled })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if cb.failures != 0 {
		t.Fatalf("expected canceled not to count as failure, got %d failures", cb.failures)
	}
	if cb.state != StateClosed {
		t.Fatalf("expected circuit to stay closed, got %v", cb.state)
	}
}

func TestCircuitBreaker_DeadlineExceededCountsAsFailure(t *testing.T) {
	cb := NewCircuitBreaker(&CircuitBreakerConfig{MaxFailures: 1, Timeout: time.Hour})
	err := cb.Call(func() error { return context.DeadlineExceeded })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if cb.state != StateOpen {
		t.Fatalf("expected deadline exceeded to open circuit, got %v", cb.state)
	}
}
