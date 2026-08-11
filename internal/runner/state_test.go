package runner

import (
	"testing"
)

func TestStateRunningCounter(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	if s.Running() {
		t.Fatal("expected not running initially")
	}
	s.Begin(gen)
	if !s.Running() {
		t.Fatal("expected running after Begin")
	}
	s.End(gen)
	if s.Running() {
		t.Fatal("expected not running after End")
	}
}

func TestStateRequestRun(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	if !s.RequestRun() {
		t.Fatal("expected first RequestRun to succeed")
	}
	select {
	case <-s.Trigger():
	default:
		t.Fatal("expected trigger in channel")
	}
	if !s.RequestRun() {
		t.Fatal("expected second RequestRun to succeed while trigger pending")
	}
	s.Begin(gen)
	if s.RequestRun() {
		t.Fatal("expected RequestRun to fail while active")
	}
	s.End(gen)
	if !s.RequestRun() {
		t.Fatal("expected RequestRun after idle")
	}
}

func TestStateReset(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.Begin(gen)
	s.Begin(gen)
	if !s.Running() {
		t.Fatal("expected running")
	}
	s.Reset()
	if s.Running() {
		t.Fatal("expected not running after Reset")
	}
	if !s.RequestRun() {
		t.Fatal("expected RequestRun after Reset")
	}
}

func TestStateRestartFlag(t *testing.T) {
	s := NewState()
	if s.ConsumeRestart() {
		t.Fatal("expected no restart initially")
	}
	s.MarkRestart()
	if !s.ConsumeRestart() {
		t.Fatal("expected restart after MarkRestart")
	}
	if s.ConsumeRestart() {
		t.Fatal("expected restart consumed once")
	}
}

func TestStateGenerationIgnoresStaleBeginEnd(t *testing.T) {
	s := NewState()
	oldGen := s.Reset()
	s.Begin(oldGen)
	if !s.Running() {
		t.Fatal("expected running on current gen")
	}
	newGen := s.Reset()
	if s.Running() {
		t.Fatal("expected not running after Reset")
	}
	s.Begin(oldGen)
	if s.Running() {
		t.Fatal("stale Begin must be ignored")
	}
	s.Begin(newGen)
	if !s.Running() {
		t.Fatal("expected running on new gen")
	}
	s.End(oldGen)
	if !s.Running() {
		t.Fatal("stale End must be ignored")
	}
	s.End(newGen)
	if s.Running() {
		t.Fatal("expected not running after End on new gen")
	}
}

func TestStateDrainTrigger(t *testing.T) {
	s := NewState()
	if !s.RequestRun() {
		t.Fatal("expected RequestRun to succeed")
	}
	s.DrainTrigger()
	select {
	case <-s.Trigger():
		t.Fatal("expected trigger drained")
	default:
	}
	s.DrainTrigger()
}

func TestRestartFlagSurvivesTriggerConsumption(t *testing.T) {
	s := NewState()
	s.MarkRestart()
	select {
	case s.trigger <- struct{}{}:
	default:
	}
	select {
	case <-s.Trigger():
	default:
		t.Fatal("expected trigger in channel")
	}
	s.Reset()
	if !s.ConsumeRestart() {
		t.Fatal("restart flag must survive trigger consumption by old runner")
	}
}
