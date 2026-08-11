package logger

import (
	"sync"
	"testing"
)

func TestRingBufferOverwrite(t *testing.T) {
	r := newRingBuffer(3)
	for i := 1; i <= 5; i++ {
		r.add(Entry{Message: string(rune('0' + i))})
	}
	got := r.snapshot(10)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	want := []string{"3", "4", "5"}
	for i, e := range got {
		if e.Message != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, e.Message, want[i])
		}
	}
}

func TestRingBufferConcurrent(t *testing.T) {
	r := newRingBuffer(100)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				r.add(Entry{Message: "x"})
			}
		}(i)
	}
	wg.Wait()
	if len(r.snapshot(200)) != 100 {
		t.Fatalf("expected full buffer after concurrent writes")
	}
}

func TestSnapshotLimit(t *testing.T) {
	r := newRingBuffer(10)
	for i := 0; i < 10; i++ {
		r.add(Entry{Message: string(rune('a' + i))})
	}
	got := r.snapshot(3)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Message != "h" || got[2].Message != "j" {
		t.Fatalf("expected last 3 entries h..j, got %v %v %v", got[0].Message, got[1].Message, got[2].Message)
	}
}
