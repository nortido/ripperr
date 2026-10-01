package helper

import (
	"errors"
	"testing"
	"time"

	"ripperr/internal/helper/breakeralerts"
)

func TestCircuitBreaker_AlertsOnOpenAndClose(t *testing.T) {
	reg := breakeralerts.New()
	cb := NewCircuitBreaker(&CircuitBreakerConfig{MaxFailures: 1, Timeout: time.Millisecond})
	cb.TrackerName = "kinozal"
	cb.Alerts = reg

	_ = cb.Call(func() error { return errors.New("boom") })
	if len(reg.List()) != 1 {
		t.Fatalf("expected alert after open, got %d", len(reg.List()))
	}
	if reg.List()[0].LastError != "boom" {
		t.Fatalf("error = %q", reg.List()[0].LastError)
	}

	time.Sleep(5 * time.Millisecond)
	_ = cb.Call(func() error { return nil }) // переход Open в HalfOpen, всё ещё заблокировано
	if err := cb.Call(func() error { return nil }); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(reg.List()) != 0 {
		t.Fatal("expected alert cleared after recovery")
	}
}
