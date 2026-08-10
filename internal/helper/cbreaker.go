package helper

import (
	"errors"
	"sync"
	"time"
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
	state           State
	failures        int
	lastFailureTime time.Time
	mx              sync.Mutex
}

func (cb *CircuitBreaker) Call(fn func() (err error)) (err error) {
	cb.mx.Lock()
	defer cb.mx.Unlock()

	switch cb.state {
	case StateClosed:
		if err = fn(); err != nil {
			cb.recordFailure()
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
			cb.recordFailure()
			return err
		}
		cb.reset()
	}
	return
}

func (cb *CircuitBreaker) recordFailure() {
	cb.failures++
	cb.lastFailureTime = time.Now()
	if cb.failures >= cb.MaxFailures {
		cb.state = StateOpen
	}
}

func (cb *CircuitBreaker) reset() {
	cb.state = StateClosed
	cb.failures = 0
}

func NewCircuitBreaker(cfg *CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		state:       StateClosed,
		MaxFailures: cfg.MaxFailures,
		Timeout:     cfg.Timeout,
	}
}
