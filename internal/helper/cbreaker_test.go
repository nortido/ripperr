package helper

import (
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

	// First call after the timeout elapses transitions Open -> HalfOpen but
	// still blocks that particular call.
	if err := cb.Call(func() error { return nil }); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen on the transition call, got %v", err)
	}
	if cb.state != StateHalfOpen {
		t.Fatalf("expected half-open after timeout elapsed, got %v", cb.state)
	}

	// The next call is a real trial call: success closes the circuit again.
	if err := cb.Call(func() error { return nil }); err != nil {
		t.Fatalf("expected successful half-open call, got %v", err)
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
