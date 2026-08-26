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
		t.Fatal("expected second RequestRun to fail while trigger buffer full")
	}
	s.Begin(gen)
	if s.RequestRun() {
		t.Fatal("expected RequestRun to fail while active")
	}
	s.End(gen)
	s.DrainTrigger()
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
	case s.trigger <- false:
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

func TestStateRequestDryRun(t *testing.T) {
	s := NewState()
	if !s.RequestDryRun() {
		t.Fatal("expected RequestDryRun to succeed")
	}
	select {
	case dry := <-s.Trigger():
		if !dry {
			t.Fatal("expected dry flag on trigger")
		}
	default:
		t.Fatal("expected queued trigger")
	}
	if !s.RequestRun() {
		t.Fatal("expected RequestRun to succeed")
	}
	select {
	case dry := <-s.Trigger():
		if dry {
			t.Fatal("expected normal run flag on trigger")
		}
	default:
		t.Fatal("expected queued trigger")
	}
}

func TestStateStoppingClearedByReset(t *testing.T) {
	s := NewState()
	if s.Stopping() {
		t.Fatal("fresh state must not be stopping")
	}
	s.MarkStopping()
	if !s.Stopping() {
		t.Fatal("expected stopping after MarkStopping")
	}
	s.Reset()
	if s.Stopping() {
		t.Fatal("Reset must clear stopping")
	}
}

func TestStateItemQueueAndCurrent(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.Begin(gen)
	s.Begin(gen)
	s.Enqueue(gen, "aaa")
	s.Enqueue(gen, "bbb")

	queued, total := s.QueuedItems(10)
	if total != 2 || len(queued) != 2 {
		t.Fatalf("queued=%v total=%d, want 2 hashes", queued, total)
	}

	s.StartItem(gen, 1, "aaa")
	queued, total = s.QueuedItems(10)
	if total != 1 || len(queued) != 1 || queued[0] != "bbb" {
		t.Fatalf("queued=%v total=%d, want only bbb", queued, total)
	}
	if cur := s.CheckingItems(); len(cur) != 1 || cur[0] != "aaa" {
		t.Fatalf("checking=%v, want [aaa]", cur)
	}

	// запись остаётся подсвеченной, пока воркер трекера не возьмёт следующую:
	// между записями он спит паузу лимита, и гасить подсветку там не за что
	s.End(gen)
	if cur := s.CheckingItems(); len(cur) != 1 || cur[0] != "aaa" {
		t.Fatalf("checking=%v after End, want [aaa]", cur)
	}

	s.StartItem(gen, 1, "bbb")
	if cur := s.CheckingItems(); len(cur) != 1 || cur[0] != "bbb" {
		t.Fatalf("checking=%v, want [bbb]", cur)
	}
}

func TestStateItemsClearedWhenRunFinishes(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.Begin(gen)
	s.Enqueue(gen, "aaa")
	s.StartItem(gen, 2, "aaa")

	s.End(gen)
	if cur := s.CheckingItems(); len(cur) != 0 {
		t.Fatalf("checking=%v after run finished, want empty", cur)
	}
	if _, total := s.QueuedItems(10); total != 0 {
		t.Fatalf("queued total=%d after run finished, want 0", total)
	}
}

func TestStateQueuedItemsCap(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	for _, h := range []string{"a", "b", "c"} {
		s.Enqueue(gen, h)
	}
	queued, total := s.QueuedItems(2)
	if total != 3 || len(queued) != 2 {
		t.Fatalf("queued=%v total=%d, want 2 of 3", queued, total)
	}
}

func TestStateUnqueueDropsPending(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.Enqueue(gen, "aaa")
	s.Unqueue(gen, "aaa")
	if _, total := s.QueuedItems(10); total != 0 {
		t.Fatalf("queued total=%d, want 0", total)
	}
}

func TestStateItemsClearedByReset(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.Begin(gen)
	s.Enqueue(gen, "aaa")
	s.StartItem(gen, 1, "aaa")
	s.Reset()
	if cur := s.CheckingItems(); len(cur) != 0 {
		t.Fatalf("checking=%v after Reset, want empty", cur)
	}
	if _, total := s.QueuedItems(10); total != 0 {
		t.Fatalf("queued total=%d after Reset, want 0", total)
	}
}

func TestStateStartItemIgnoresStaleGeneration(t *testing.T) {
	s := NewState()
	stale := s.Reset()
	s.Reset()
	s.StartItem(stale, 1, "aaa")
	s.Enqueue(stale, "bbb")
	if cur := s.CheckingItems(); len(cur) != 0 {
		t.Fatalf("checking=%v after stale StartItem, want empty", cur)
	}
	if _, total := s.QueuedItems(10); total != 0 {
		t.Fatalf("queued total=%d after stale Enqueue, want 0", total)
	}
}
