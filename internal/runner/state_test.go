package runner

import (
	"context"
	"encoding/json"
	"errors"
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
	s.Enqueue(gen, "aaa", false)
	s.Enqueue(gen, "bbb", false)

	queued, total := s.QueuedItems(10)
	if total != 2 || len(queued) != 2 {
		t.Fatalf("queued=%v total=%d, want 2 hashes", queued, total)
	}

	s.StartItem(gen, 1, "aaa")
	queued, total = s.QueuedItems(10)
	if total != 2 || len(queued) != 1 || queued[0] != "bbb" {
		t.Fatalf("queued=%v total=%d, want only bbb listed and total 2 (waiting + checking)", queued, total)
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
	s.FinishItem(gen, 1, "aaa")
	if cur := s.CheckingItems(); len(cur) != 0 {
		t.Fatalf("checking=%v after FinishItem, want empty", cur)
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
	s.Enqueue(gen, "aaa", false)
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
		s.Enqueue(gen, h, false)
	}
	queued, total := s.QueuedItems(2)
	if total != 3 || len(queued) != 2 {
		t.Fatalf("queued=%v total=%d, want 2 of 3", queued, total)
	}
}

func TestStateUnqueueDropsPending(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.Enqueue(gen, "aaa", false)
	s.Unqueue(gen, "aaa", false)
	if _, total := s.QueuedItems(10); total != 0 {
		t.Fatalf("queued total=%d, want 0", total)
	}
}

func TestStateItemsClearedByReset(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.Begin(gen)
	s.Enqueue(gen, "aaa", false)
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
	s.Enqueue(stale, "bbb", false)
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

func TestPollNewWithoutPoller(t *testing.T) {
	var nilState *State
	if _, err := nilState.PollNew(context.Background()); !errors.Is(err, ErrPollUnavailable) {
		t.Fatalf("nil state: err = %v, want ErrPollUnavailable", err)
	}
	nilState.SetPoller(1, func(context.Context) (int, error) { return 1, nil })
	s := NewState()
	if _, err := s.PollNew(context.Background()); !errors.Is(err, ErrPollUnavailable) {
		t.Fatalf("no poller: err = %v, want ErrPollUnavailable", err)
	}
}

func TestPollNewSerializesAndReuses(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	var calls, running, maxRunning atomic.Int32
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	s.SetPoller(gen, func(context.Context) (int, error) {
		calls.Add(1)
		if n := running.Add(1); n > maxRunning.Load() {
			maxRunning.Store(n)
		}
		entered <- struct{}{}
		<-release
		running.Add(-1)
		return 2, nil
	})

	var wg sync.WaitGroup
	got := make([]int, 3)
	errs := make([]error, 3)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = s.PollNew(context.Background())
		}()
	}
	<-entered
	close(release)
	wg.Wait()
	if calls.Load() != 1 || maxRunning.Load() != 1 {
		t.Fatalf("calls = %d, max parallel = %d, want 1 and 1", calls.Load(), maxRunning.Load())
	}
	for i := range got {
		if errs[i] != nil || got[i] != 2 {
			t.Fatalf("caller %d: n = %d, err = %v, want 2, nil", i, got[i], errs[i])
		}
	}

	old := pollMinGap
	pollMinGap = 0
	t.Cleanup(func() { pollMinGap = old })
	if n, err := s.PollNew(context.Background()); err != nil || n != 2 {
		t.Fatalf("after gap: n = %d, err = %v", n, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 once the reuse window is over", calls.Load())
	}
}

func TestPollNewErrorNotCached(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	boom := errors.New("boom")
	var calls int
	s.SetPoller(gen, func(context.Context) (int, error) {
		calls++
		if calls == 1 {
			return 0, boom
		}
		return 3, nil
	})
	if _, err := s.PollNew(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("first: err = %v, want boom", err)
	}
	if n, err := s.PollNew(context.Background()); err != nil || n != 3 {
		t.Fatalf("second: n = %d, err = %v, want 3, nil", n, err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestSetPollerGeneration(t *testing.T) {
	ctx := context.Background()
	s := NewState()
	old := s.Reset()
	cur := s.Reset()
	s.SetPoller(old, func(context.Context) (int, error) { return 1, nil })
	if _, err := s.PollNew(ctx); !errors.Is(err, ErrPollUnavailable) {
		t.Fatalf("stale gen registered: err = %v", err)
	}

	s.SetPoller(cur, func(context.Context) (int, error) { return 2, nil })
	if n, err := s.PollNew(ctx); err != nil || n != 2 {
		t.Fatalf("n = %d, err = %v, want 2, nil", n, err)
	}
	s.SetPoller(old, nil)
	if n, err := s.PollNew(ctx); err != nil || n != 2 {
		t.Fatalf("old gen nil dropped the new poller: n = %d, err = %v", n, err)
	}

	next := s.Reset()
	s.SetPoller(next, func(context.Context) (int, error) { return 7, nil })
	if n, err := s.PollNew(ctx); err != nil || n != 7 {
		t.Fatalf("new poller served from the old cache: n = %d, err = %v", n, err)
	}

	s.SetPoller(next, nil)
	if _, err := s.PollNew(ctx); !errors.Is(err, ErrPollUnavailable) {
		t.Fatalf("own nil: err = %v, want ErrPollUnavailable", err)
	}
}

func TestSetPollerDoesNotWaitForPoll(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	entered := make(chan struct{})
	release := make(chan struct{})
	s.SetPoller(gen, func(context.Context) (int, error) {
		close(entered)
		<-release
		return 1, nil
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.PollNew(context.Background())
	}()
	<-entered

	returned := make(chan struct{})
	go func() {
		s.SetPoller(gen, nil)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Error("SetPoller waited for the running poll")
	}
	close(release)
	<-done
	<-returned
}

func absentSet(hashes ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(hashes))
	for _, h := range hashes {
		m[h] = struct{}{}
	}
	return m
}

func TestSetAbsent(t *testing.T) {
	poller := func(context.Context) (int, error) { return 0, nil }
	same := func(t *testing.T, s *State, want map[string]struct{}) {
		t.Helper()
		got := s.Absent()
		if len(got) != len(want) {
			t.Fatalf("Absent = %v, want %v", got, want)
		}
		for h := range want {
			if _, ok := got[h]; !ok {
				t.Fatalf("Absent = %v, want %v", got, want)
			}
		}
	}

	var nilState *State
	if n := nilState.SetAbsent(1, absentSet("a")); n != 0 {
		t.Fatalf("nil State: flipped = %d, want 0", n)
	}
	if got := nilState.Absent(); got != nil {
		t.Fatalf("nil State: Absent = %v, want nil", got)
	}

	s := NewState()
	gen := s.Reset()
	if n := s.SetAbsent(gen, absentSet("a")); n != 0 {
		t.Fatalf("no poller: flipped = %d, want 0", n)
	}
	same(t, s, nil)

	s.SetPoller(gen, poller)
	if n := s.SetAbsent(gen, absentSet("a", "b")); n != 2 {
		t.Fatalf("first set: flipped = %d, want 2", n)
	}
	if n := s.SetAbsent(gen, absentSet("b", "c")); n != 2 {
		t.Fatalf("shift: flipped = %d, want 2", n)
	}
	same(t, s, absentSet("b", "c"))
	if n := s.SetAbsent(gen, absentSet("b", "c")); n != 0 {
		t.Fatalf("same set: flipped = %d, want 0", n)
	}

	in := absentSet("p")
	s.SetAbsent(gen, in)
	in["q"] = struct{}{}
	same(t, s, absentSet("p"))

	gen2 := s.Reset()
	s.SetPoller(gen2, poller)
	same(t, s, nil)
	if n := s.SetAbsent(gen, absentSet("x")); n != 0 {
		t.Fatalf("old gen: flipped = %d, want 0", n)
	}
	same(t, s, nil)

	s.SetAbsent(gen2, absentSet("y"))
	s.SetPoller(gen2, nil)
	same(t, s, nil)

	s.SetPoller(gen2, poller)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			s.SetAbsent(gen2, absentSet("r", fmt.Sprint(i)))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			for range s.Absent() {
			}
		}
	}()
	wg.Wait()
}

type saveRec struct {
	calls int
	key   string
	raw   json.RawMessage
}

func (r *saveRec) save(_ context.Context, key string, raw json.RawMessage) error {
	r.calls++
	r.key, r.raw = key, raw
	return nil
}

func TestStateServerErrorReturnValues(t *testing.T) {
	s := NewState()
	now := time.Now()
	if s.PageServerError(1, "a", now) {
		t.Fatal("5xx with unknown tracker state did not count")
	}
	if s.PageOK(1, "a") {
		t.Fatal("PageOK without a mark cleared nothing")
	}
	if !s.PageServerError(1, "a", now) {
		t.Fatal("5xx after a success counts")
	}
	if !s.PageOK(1, "a") {
		t.Fatal("PageOK must report the cleared mark")
	}
}

func TestStateRestoreServerErrorsTTL(t *testing.T) {
	now := time.Now()
	raw := func(m map[string]serverErrJSON) json.RawMessage {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	s := NewState()
	err := s.RestoreServerErrors(raw(map[string]serverErrJSON{
		"young":  {Count: 2, At: now.Add(-serverErrTTL + time.Hour)},
		"old":    {Count: 2, At: now.Add(-serverErrTTL - time.Hour)},
		"zero":   {Count: 0, At: now},
		"noTime": {Count: 3},
	}), now)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := s.ServerErrorMark("young"); !ok || m.Streak != 2 {
		t.Fatalf("young = %+v ok = %v, want count 2", m, ok)
	}
	for _, h := range []string{"old", "zero", "noTime"} {
		if _, ok := s.ServerErrorMark(h); ok {
			t.Fatalf("%s must not be restored", h)
		}
	}
}

func TestStateRestoreServerErrorsOnce(t *testing.T) {
	now := time.Now()
	s := NewState()
	s.PageOK(1, "x")
	s.PageServerError(1, "a", now)
	first := json.RawMessage(`{"a":{"count":9,"at":"` + now.Format(time.RFC3339Nano) + `"},"b":{"count":1,"at":"` + now.Format(time.RFC3339Nano) + `"}}`)
	if err := s.RestoreServerErrors(first, now); err != nil {
		t.Fatal(err)
	}
	if m, _ := s.ServerErrorMark("a"); m.Streak != 1 {
		t.Fatalf("a = %d, a mark already in memory must not be overwritten", m.Streak)
	}
	if _, ok := s.ServerErrorMark("b"); !ok {
		t.Fatal("b must be restored")
	}
	second := json.RawMessage(`{"c":{"count":1,"at":"` + now.Format(time.RFC3339Nano) + `"}}`)
	if err := s.RestoreServerErrors(second, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ServerErrorMark("c"); ok {
		t.Fatal("a second restore must be ignored")
	}
}

func TestStateRestoreServerErrorsBadJSON(t *testing.T) {
	now := time.Now()
	s := NewState()
	if err := s.RestoreServerErrors(json.RawMessage(`{bad`), now); err == nil {
		t.Fatal("bad JSON must return an error")
	}
	good := json.RawMessage(`{"a":{"count":1,"at":"` + now.Format(time.RFC3339Nano) + `"}}`)
	if err := s.RestoreServerErrors(good, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ServerErrorMark("a"); ok {
		t.Fatal("after a bad restore the later one is ignored")
	}
}

func TestStateServerErrorsPersistNilSafe(t *testing.T) {
	var s *State
	if err := s.RestoreServerErrors(json.RawMessage(`{}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	rec := &saveRec{}
	if err := s.SaveServerErrors(context.Background(), time.Now(), rec.save); err != nil || rec.calls != 0 {
		t.Fatalf("nil state: err = %v calls = %d, want none", err, rec.calls)
	}
}

func TestStateSaveServerErrorsPrunes(t *testing.T) {
	now := time.Now()
	s := NewState()
	_ = s.RestoreServerErrors(nil, now)
	s.PageOK(1, "x")
	s.PageServerError(1, "old", now.Add(-serverErrTTL-time.Hour))
	s.PageOK(1, "x")
	s.PageServerError(1, "young", now)
	rec := &saveRec{}
	if err := s.SaveServerErrors(context.Background(), now, rec.save); err != nil {
		t.Fatal(err)
	}
	var got map[string]serverErrJSON
	if err := json.Unmarshal(rec.raw, &got); err != nil {
		t.Fatal(err)
	}
	if rec.key != serverErrsSetting || len(got) != 1 || got["young"].Count != 1 {
		t.Fatalf("key = %q saved = %+v, want only young", rec.key, got)
	}
	if _, ok := s.ServerErrorMark("old"); ok {
		t.Fatal("the old mark must be pruned from memory too")
	}
}

func TestStateSaveServerErrorsOrdered(t *testing.T) {
	s := NewState()
	now := time.Now()
	_ = s.RestoreServerErrors(nil, now)
	var last atomic.Int32
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.PageOK(1, "x")
			s.PageServerError(1, "h", now)
			_ = s.SaveServerErrors(context.Background(), now, func(_ context.Context, _ string, raw json.RawMessage) error {
				var m map[string]serverErrJSON
				if err := json.Unmarshal(raw, &m); err != nil {
					return err
				}
				if int32(m["h"].Count) < last.Load() {
					t.Errorf("worker %d stored an older snapshot after a newer one", i)
				}
				last.Store(int32(m["h"].Count))
				return nil
			})
		}()
	}
	wg.Wait()
}

func TestStateSaveServerErrorsWaitsForRestore(t *testing.T) {
	now := time.Now()
	s := NewState()
	s.PageOK(1, "x")
	s.PageServerError(1, "a", now)
	rec := &saveRec{}
	if err := s.SaveServerErrors(context.Background(), now, rec.save); err != nil || rec.calls != 0 {
		t.Fatalf("err = %v calls = %d, want no save before a restore", err, rec.calls)
	}
	_ = s.RestoreServerErrors(nil, now)
	if err := s.SaveServerErrors(context.Background(), now, rec.save); err != nil || rec.calls != 1 {
		t.Fatalf("err = %v calls = %d, want one save after a restore", err, rec.calls)
	}
}

func TestStateUnforceDropsForcedQueue(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.Enqueue(gen, "a", true)
	s.Enqueue(gen, "b", false)
	s.Enqueue(gen, "c", true)
	s.Enqueue(gen, "c", false)
	if _, total := s.QueuedItems(10); total != 3 {
		t.Fatalf("queued total=%d, want 3", total)
	}

	s.Unforce("a")
	s.Unforce("b")
	s.Unforce("c")
	queued, total := s.QueuedItems(10)
	if total != 2 || len(queued) != 2 {
		t.Fatalf("queued=%v total=%d, want b and c (normal tasks stay)", queued, total)
	}
	if !s.ForceCancelled("a") {
		t.Fatal("ForceCancelled(a) = false after Unforce")
	}

	if !s.RequestForce("a") {
		t.Fatal("RequestForce(a) was not accepted")
	}
	if s.ForceCancelled("a") {
		t.Fatal("ForceCancelled(a) = true after RequestForce, want mark cleared")
	}

	s.Unforce("d")
	s.Enqueue(gen, "d", true)
	if _, total := s.QueuedItems(10); total != 2 {
		t.Fatalf("queued total=%d after forced Enqueue of an unforced hash, want 2", total)
	}
}

func TestStateUnforceMarkSurvivesEndAndReset(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.Begin(gen)
	s.Unforce("a")
	s.End(gen)
	if !s.ForceCancelled("a") {
		t.Fatal("mark lost after End to zero")
	}
	s.Reset()
	if !s.ForceCancelled("a") {
		t.Fatal("mark lost after Reset")
	}
}

func TestStateFinishItemClearsHighlight(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.StartItem(gen, 1, "a")
	s.FinishItem(gen, 1, "a")
	if cur := s.CheckingItems(); len(cur) != 0 {
		t.Fatalf("checking=%v after FinishItem, want empty", cur)
	}

	s.StartItem(gen, 1, "b")
	s.FinishItem(gen, 1, "x")
	if cur := s.CheckingItems(); len(cur) != 1 || cur[0] != "b" {
		t.Fatalf("checking=%v after FinishItem of another hash, want [b]", cur)
	}
}

func TestStateLeftCountsChecking(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.Enqueue(gen, "a", false)
	s.Enqueue(gen, "b", false)
	s.StartItem(gen, 1, "a")
	if _, total := s.QueuedItems(10); total != 2 {
		t.Fatalf("total=%d with one waiting and one checking, want 2", total)
	}
	s.FinishItem(gen, 1, "a")
	if _, total := s.QueuedItems(10); total != 1 {
		t.Fatalf("total=%d after FinishItem, want 1", total)
	}
}

func TestStateUnforceWakes(t *testing.T) {
	s := NewState()
	w := s.UnforceWake()
	if w == nil {
		t.Fatal("UnforceWake returned nil for a live state")
	}
	select {
	case <-w:
		t.Fatal("wake channel closed before any Unforce")
	default:
	}
	s.Unforce("a")
	select {
	case <-w:
	default:
		t.Fatal("Unforce must close the wake channel")
	}
	w2 := s.UnforceWake()
	select {
	case <-w2:
		t.Fatal("the channel after Unforce must be a new open one")
	default:
	}
	s.Unforce("b")
	select {
	case <-w2:
	default:
		t.Fatal("second Unforce must close the new channel")
	}
	if got := (*State)(nil).UnforceWake(); got != nil {
		t.Fatalf("nil state wake = %v, want nil", got)
	}
}

func TestStateQueuedItemsDedupesChecking(t *testing.T) {
	s := NewState()
	gen := s.Reset()
	s.Enqueue(gen, "a", false)
	s.StartItem(gen, 1, "a")
	s.Enqueue(gen, "a", false)
	hashes, total := s.QueuedItems(10)
	if total != 1 || len(hashes) != 1 || hashes[0] != "a" {
		t.Fatalf("hashes=%v total=%d with one hash queued and checking, want [a] and 1", hashes, total)
	}

	s.StartItem(gen, 2, "x")
	s.StartItem(gen, 3, "x")
	if _, total := s.QueuedItems(10); total != 2 {
		t.Fatalf("total=%d with one hash checking on two trackers, want 2", total)
	}
}
