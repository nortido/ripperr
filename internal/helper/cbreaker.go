package helper

import (
	"errors"
	"sync"
	"time"

	"trackerr/internal/helper/breakeralerts"
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

// ErrCircuitOpen is returned by Call while the circuit is open and blocking calls.
var ErrCircuitOpen = errors.New("circuit breaker: circuit is open")

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
			cb.recordFailure(err)
			return err
		}
		cb.reset()
	case StateOpen:
		if time.Since(cb.lastFailureTime) > cb.Timeout {
			cb.state = StateHalfOpen
		}
		err = ErrCircuitOpen
	case StateHalfOpen:
		if err = fn(); err != nil {
			cb.recordFailure(err)
			return err
		}
		cb.reset()
	}
	return
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
	wasTripped := cb.state == StateOpen || cb.state == StateHalfOpen
	cb.state = StateClosed
	cb.failures = 0
	cb.lastErr = nil
	if wasTripped {
		cb.clearAlert()
	}
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
