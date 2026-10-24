package helper

import (
	"context"
	"errors"
	"sync"
	"time"

	"ripperr/internal/helper/breakeralerts"
)

type State int

type CircuitBreakerConfig struct {
	MaxFailures int
	Timeout     time.Duration
}

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

var ErrCircuitOpen = errors.New("circuit breaker: circuit is open")

type NeutralError struct{ Err error }

func (e *NeutralError) Error() string { return e.Err.Error() }

func (e *NeutralError) Unwrap() error { return e.Err }

type CircuitBreaker struct {
	MaxFailures     int
	Timeout         time.Duration
	TrackerName     string
	Alerts          *breakeralerts.Registry
	state           State
	failures        int
	lastFailureTime time.Time
	lastErr         error
	mx              sync.Mutex
}

func (cb *CircuitBreaker) Call(fn func() (err error)) (err error) {
	cb.mx.Lock()
	defer cb.mx.Unlock()

	switch cb.state {
	case StateClosed:
		if err = fn(); err != nil {
			if cb.neutral(err) {
				return err
			}
			cb.recordFailure(err)
			return err
		}
		cb.reset()
	case StateOpen:
		if time.Since(cb.lastFailureTime) <= cb.Timeout {
			return ErrCircuitOpen
		}
		cb.state = StateHalfOpen
		fallthrough
	case StateHalfOpen:
		if err = fn(); err != nil {
			if cb.neutral(err) {
				return err
			}
			cb.recordFailure(err)
			return err
		}
		cb.reset()
	}
	return
}

func (cb *CircuitBreaker) neutral(err error) bool {
	var ne *NeutralError
	return errors.Is(err, context.Canceled) || errors.As(err, &ne)
}

func (cb *CircuitBreaker) recordFailure(err error) {
	wasOpen := cb.state == StateOpen
	cb.failures++
	cb.lastFailureTime = time.Now()
	if err != nil {
		cb.lastErr = err
	}
	if cb.failures >= cb.MaxFailures {
		cb.state = StateOpen
	}
	cb.notifyAlerts(wasOpen)
}

func (cb *CircuitBreaker) reset() {
	cb.state = StateClosed
	cb.failures = 0
	cb.lastErr = nil
	// инцидент мог достаться от прошлого экземпляра брейкера, поэтому закрываем после каждого успеха
	cb.clearAlert()
}

func (cb *CircuitBreaker) notifyAlerts(wasOpen bool) {
	if cb.Alerts == nil || cb.TrackerName == "" || cb.state != StateOpen {
		return
	}
	msg := ""
	if cb.lastErr != nil {
		msg = cb.lastErr.Error()
	}
	if !wasOpen || msg != "" {
		cb.Alerts.Open(cb.TrackerName, msg)
	}
}

func (cb *CircuitBreaker) clearAlert() {
	if cb.Alerts != nil && cb.TrackerName != "" {
		cb.Alerts.Close(cb.TrackerName)
	}
}

func NewCircuitBreaker(cfg *CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		state:       StateClosed,
		MaxFailures: cfg.MaxFailures,
		Timeout:     cfg.Timeout,
	}
}
