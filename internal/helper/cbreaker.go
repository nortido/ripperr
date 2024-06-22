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
	case StateOpen:
		if err = fn(); err != nil {
			cb.recordFailure()
			return err
		}
	case StateClosed:
		if time.Since(cb.lastFailureTime) > cb.Timeout {
			cb.state = StateHalfOpen
		}
		err = errors.New("failed to call")
		return
	case StateHalfOpen:
		if err = fn(); err != nil {
			cb.recordFailure()
			return err
		}
		cb.reset()
		return
	}
	return
}

func (cb *CircuitBreaker) recordFailure() {
	cb.failures++
	cb.lastFailureTime = time.Now()
	if cb.failures >= cb.MaxFailures {
		cb.state = StateClosed
	}
}

func (cb *CircuitBreaker) reset() {
	cb.state = StateOpen
	cb.failures = 0
}

func NewCircuitBreaker(cfg *CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		state:       StateOpen,
		MaxFailures: cfg.MaxFailures,
		Timeout:     cfg.Timeout,
	}
}
