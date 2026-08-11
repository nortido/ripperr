package runner

import (
	"sync/atomic"
)

// State is shared across reloads: manual run trigger, activity counter, restart flag.
type State struct {
	trigger chan struct{}
	active  atomic.Int32
	gen     atomic.Uint32
	restart atomic.Bool
}

func NewState() *State {
	return &State{trigger: make(chan struct{}, 1)}
}

func (s *State) Running() bool {
	return s != nil && s.active.Load() > 0
}

func (s *State) Begin(gen uint32) {
	if s != nil && gen == s.gen.Load() {
		s.active.Add(1)
	}
}

func (s *State) End(gen uint32) {
	if s != nil && gen == s.gen.Load() {
		s.active.Add(-1)
	}
}

func (s *State) Trigger() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.trigger
}

// RequestRun queues a manual run if nothing is active.
func (s *State) RequestRun() bool {
	if s == nil || s.Running() {
		return false
	}
	select {
	case s.trigger <- struct{}{}:
		return true
	default:
		return false
	}
}

// Reset clears the activity counter and bumps generation so stale Begin/End are ignored.
// Returns the new generation for the current runner instance.
func (s *State) Reset() uint32 {
	if s == nil {
		return 0
	}
	s.active.Store(0)
	return s.gen.Add(1)
}

// MarkRestart requests an immediate run after the next handler.Run start (survives reload).
func (s *State) MarkRestart() {
	if s != nil {
		s.restart.Store(true)
	}
}

// ConsumeRestart returns true once if a restart was marked since the last consume.
func (s *State) ConsumeRestart() bool {
	if s == nil {
		return false
	}
	return s.restart.Swap(false)
}

// DrainTrigger discards any pending manual run in the trigger buffer.
func (s *State) DrainTrigger() {
	if s == nil {
		return
	}
	select {
	case <-s.trigger:
	default:
	}
}
