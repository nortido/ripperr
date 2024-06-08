package helper

import (
	"errors"
	"sync"
	"time"
)

type State int

const (
	StateClosed State = iota
	StateOpen
	StateHalfOpen
)

type CircuitBreaker struct {
	state           State
	failures        int
	maxFailures     int
	timeout         time.Duration
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
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.state = StateHalfOpen
		}
		err = errors.New("call failed")
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
	if cb.failures >= cb.maxFailures {
		cb.state = StateClosed
	}
}

func (cb *CircuitBreaker) reset() {
	cb.state = StateOpen
	cb.failures = 0
}

func NewCircuitBreaker(maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:       StateOpen,
		maxFailures: maxFailures,
		timeout:     resetTimeout,
	}
}
