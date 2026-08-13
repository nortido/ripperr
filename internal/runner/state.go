package runner

import (
	"sync/atomic"
)

// State is shared across reloads: manual run trigger, activity counter, restart flag.
type State struct {
	trigger  chan bool
	active   atomic.Int32
	gen      atomic.Uint32
	restart  atomic.Bool
	dry      atomic.Bool
	stopping atomic.Bool
}

func NewState() *State {
	return &State{trigger: make(chan bool, 1)}
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

func (s *State) Trigger() <-chan bool {
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
	case s.trigger <- false:
		return true
	default:
		return false
	}
}

// RequestDryRun queues a manual dry run if nothing is active.
func (s *State) RequestDryRun() bool {
	if s == nil || s.Running() {
		return false
	}
	select {
	case s.trigger <- true:
		return true
	default:
		return false
	}
}

// DryRunning reports whether the current run is a dry run.
func (s *State) DryRunning() bool {
	return s != nil && s.dry.Load()
}

func (s *State) setDry(v bool) {
	if s != nil {
		s.dry.Store(v)
	}
}

// MarkStopping records that a stop was requested; cleared when the next runner starts.
func (s *State) MarkStopping() {
	if s != nil {
		s.stopping.Store(true)
	}
}

// Stopping reports whether a stop was requested and the runner has not restarted yet.
func (s *State) Stopping() bool {
	return s != nil && s.stopping.Load()
}

// Reset clears the activity counter and bumps generation so stale Begin/End are ignored.
// Returns the new generation for the current runner instance.
func (s *State) Reset() uint32 {
	if s == nil {
		return 0
	}
	s.active.Store(0)
	s.dry.Store(false)
	s.stopping.Store(false)
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
