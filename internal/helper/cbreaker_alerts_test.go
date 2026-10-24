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
	_ = cb.Call(func() error { return nil }) // переход Open в HalfOpen, еще заблокировано
	if err := cb.Call(func() error { return nil }); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(reg.List()) != 0 {
		t.Fatal("expected alert cleared after recovery")
	}
}

func TestCircuitBreaker_SuccessClosesInheritedIncident(t *testing.T) {
	reg := breakeralerts.New()
	type ev struct {
		open    bool
		tracker string
	}
	var events []ev
	reg.SetHook(func(open bool, tracker, _ string) { events = append(events, ev{open, tracker}) })
	reg.Open("kinozal", "boom")
	events = nil

	cb := NewCircuitBreaker(&CircuitBreakerConfig{MaxFailures: 1, Timeout: time.Minute})
	cb.TrackerName = "kinozal"
	cb.Alerts = reg

	if err := cb.Call(func() error { return nil }); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(reg.List()) != 0 {
		t.Fatalf("incident stays: %+v", reg.List())
	}
	if len(events) != 1 || events[0].open || events[0].tracker != "kinozal" {
		t.Fatalf("events = %+v", events)
	}
}
