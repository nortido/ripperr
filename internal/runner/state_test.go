package runner

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ripperr/internal/system/logger"
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

	// запись остается подсвеченной, пока воркер трекера не возьмет следующую.
	// Между записями он спит паузу лимита, гасить подсветку там незачем
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

func countLogged(msg string) int {
	n := 0
	for _, e := range logger.Snapshot(0) {
		if e.Message == msg {
			n++
		}
	}
	return n
}

func TestStateLogsPassTransitions(t *testing.T) {
	started0 := countLogged("check pass started")
	finished0 := countLogged("check pass finished")
	reset0 := countLogged("check pass finished (reset)")

	s := NewState()
	gen := s.Reset()
	s.Begin(gen)
	s.Begin(gen)
	s.End(gen)
	s.End(gen)
	if got := countLogged("check pass started") - started0; got != 1 {
		t.Fatalf("started lines = %d, want 1", got)
	}
	if got := countLogged("check pass finished") - finished0; got != 1 {
		t.Fatalf("finished lines = %d, want 1", got)
	}
	if got := countLogged("check pass finished (reset)") - reset0; got != 0 {
		t.Fatalf("reset lines after idle Reset = %d, want 0", got)
	}

	s.Begin(gen)
	s.Reset()
	if got := countLogged("check pass finished (reset)") - reset0; got != 1 {
		t.Fatalf("reset lines = %d, want 1", got)
	}

	started1 := countLogged("check pass started")
	finished1 := countLogged("check pass finished")
	s.End(gen)
	if countLogged("check pass started") != started1 || countLogged("check pass finished") != finished1 {
		t.Fatal("stale End must not log")
	}
}

func hammerState(t *testing.T, begin, end func(s *State, gen uint32), active func(s *State) int32) {
	t.Helper()
	// иначе 20000 строк "check pass" вытеснят из кольцевого буфера логов то, что считают соседние тесты
	logger.SetLevel("warn")
	defer logger.SetLevel("info")
	s := NewState()
	s.Reset()
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				g := s.gen.Load()
				begin(s, g)
				end(s, g)
			}
		}()
	}
	var negative atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			if active(s) < 0 {
				negative.Store(true)
			}
		}
	}()
	for range 20000 {
		s.Reset()
	}
	stop.Store(true)
	wg.Wait()
	if negative.Load() {
		t.Fatal("pass counter went negative")
	}
	if got := active(s); got != 0 {
		t.Fatalf("counter after balanced Begin/End = %d, want 0", got)
	}
	cur := s.gen.Load()
	begin(s, cur)
	if active(s) != 1 {
		t.Fatalf("counter after Begin(cur) = %d, want 1", active(s))
	}
	end(s, cur)
	if active(s) != 0 {
		t.Fatalf("counter after End(cur) = %d, want 0", active(s))
	}
}

func TestStateResetRaceKeepsCountBalanced(t *testing.T) {
	hammerState(t,
		func(s *State, g uint32) { s.Begin(g) },
		func(s *State, g uint32) { s.End(g) },
		func(s *State) int32 { return s.active.Load() })
}

func TestStateResetRaceKeepsNormalCountBalanced(t *testing.T) {
	hammerState(t,
		func(s *State, g uint32) { s.BeginNormal(g) },
		func(s *State, g uint32) { s.EndNormal(g) },
		func(s *State) int32 { return s.normalActive.Load() })
}

func TestRunRequestStateWaitingFlag(t *testing.T) {
	s := NewState()
	if s.WaitingSettings() {
		t.Fatal("waiting at start")
	}
	s.SetWaitingSettings(true)
	if !s.WaitingSettings() {
		t.Fatal("flag not set")
	}
	s.SetWaitingSettings(false)
	if s.WaitingSettings() {
		t.Fatal("flag not cleared")
	}
	var nilState *State
	nilState.SetWaitingSettings(true)
	if nilState.WaitingSettings() {
		t.Fatal("nil state must never wait")
	}
}

func TestStateNormalCounter(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	if s.NormalRunning() {
		t.Fatal("normal at start")
	}
	s.BeginNormal(gen)
	if !s.NormalRunning() {
		t.Fatal("expected normal running")
	}
	s.EndNormal(gen)
	if s.NormalRunning() {
		t.Fatal("expected idle after EndNormal")
	}
	s.BeginNormal(gen)
	s.Reset()
	if s.NormalRunning() {
		t.Fatal("Reset must zero the normal counter")
	}
	s.EndNormal(gen)
	if s.NormalRunning() {
		t.Fatal("stale EndNormal must be ignored")
	}
}

func TestStateTorrentLock(t *testing.T) {
	s := NewState()
	unlock, ok := s.TryLockTorrent("ABCD")
	if !ok {
		t.Fatal("first TryLockTorrent must succeed")
	}
	if _, ok := s.TryLockTorrent("abcd"); ok {
		t.Fatal("same hash in another case must be busy")
	}
	unlock()
	unlock2, ok := s.TryLockTorrent("abcd")
	if !ok {
		t.Fatal("TryLockTorrent after unlock must succeed")
	}
	got := make(chan struct{})
	go func() {
		s.LockTorrent("ABCD")()
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("LockTorrent must wait while the hash is held")
	case <-time.After(50 * time.Millisecond):
	}
	unlock2()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("LockTorrent did not proceed after unlock")
	}

	var nilState *State
	nu, ok := nilState.TryLockTorrent("x")
	if !ok || nu == nil {
		t.Fatal("nil state: TryLockTorrent must be ok with a callable unlock")
	}
	nu()
	nilState.LockTorrent("x")()
}

func TestStateTorrentLockLeavesNoEntries(t *testing.T) {
	s := NewState()
	for i := range 50 {
		hash := fmt.Sprintf("H%02d", i)
		unlock := s.LockTorrent(hash)
		if _, ok := s.TryLockTorrent(hash); ok {
			t.Fatalf("%s: second lock must fail while held", hash)
		}
		unlock()
		again, ok := s.TryLockTorrent(hash)
		if !ok {
			t.Fatalf("%s: lock must succeed after unlock", hash)
		}
		again()
	}
	s.torrents.mu.Lock()
	defer s.torrents.mu.Unlock()
	if n := len(s.torrents.m); n != 0 {
		t.Fatalf("%d lock entries left after all unlocks", n)
	}
}

func TestStateServerErrorsNilSafe(t *testing.T) {
	var s *State
	s.PageServerError(1, "h", time.Now())
	s.PageOK(1, "h")
	if _, ok := s.ServerErrorMark("h"); ok {
		t.Fatal("nil state must hold no marks")
	}
}

func TestStateServerErrorIsolation(t *testing.T) {
	s := NewState()
	now := time.Now()
	s.PageServerError(1, "a", now)
	if _, ok := s.ServerErrorMark("a"); ok {
		t.Fatal("5xx with unknown tracker state must not count")
	}
	s.PageOK(1, "x")
	s.PageServerError(1, "a", now)
	s.PageServerError(1, "b", now)
	if m, ok := s.ServerErrorMark("a"); !ok || m.Streak != 1 || !m.CheckedAt.Equal(now) {
		t.Fatalf("a = %+v ok = %v, want count 1 at now", m, ok)
	}
	if _, ok := s.ServerErrorMark("b"); ok {
		t.Fatal("second 5xx without a success in between must not count")
	}
	s.PageOK(1, "a")
	if _, ok := s.ServerErrorMark("a"); ok {
		t.Fatal("page success must clear the mark")
	}
}
