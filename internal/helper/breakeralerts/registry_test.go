package breakeralerts

import (
	"fmt"
	"sync"
	"testing"
)

func TestRegistryAddRemove(t *testing.T) {
	r := New()
	r.Open("kinozal", "timeout")
	list := r.List()
	if len(list) != 1 || list[0].Tracker != "kinozal" || list[0].LastError != "timeout" {
		t.Fatalf("list = %+v", list)
	}
	r.Open("kinozal", "still down")
	list = r.List()
	if list[0].LastError != "still down" {
		t.Fatalf("error not updated: %+v", list[0])
	}
	r.Close("kinozal")
	if len(r.List()) != 0 {
		t.Fatal("expected empty after close")
	}
}

func TestRegistryMultipleTrackers(t *testing.T) {
	r := New()
	r.Open("kinozal", "a")
	r.Open("rutracker", "b")
	if len(r.List()) != 2 {
		t.Fatalf("expected 2 incidents")
	}
	r.Close("kinozal")
	list := r.List()
	if len(list) != 1 || list[0].Tracker != "rutracker" {
		t.Fatalf("list = %+v", list)
	}
}

func TestRegistryHookFiresOnlyOnTransition(t *testing.T) {
	r := New()
	var events []string
	r.SetHook(func(open bool, tracker, errMsg string) {
		events = append(events, fmt.Sprintf("%v:%s:%s", open, tracker, errMsg))
	})

	r.Open("kinozal", "timeout")
	r.Open("kinozal", "still down") // update while already open: no transition
	r.Close("kinozal")
	r.Close("kinozal") // already gone: no transition

	want := []string{"true:kinozal:timeout", "false:kinozal:"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("events[%d] = %q, want %q", i, events[i], want[i])
		}
	}
}

func TestRegistryHookConcurrentNoRace(t *testing.T) {
	r := New()
	var mu sync.Mutex
	calls := 0
	r.SetHook(func(open bool, tracker, errMsg string) {
		mu.Lock()
		calls++
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tracker := fmt.Sprintf("t%d", i%5)
			r.Open(tracker, "err")
			r.List()
			r.Close(tracker)
		}(i)
	}
	wg.Wait()
}
