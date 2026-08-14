package logger

import (
	"sync"
	"time"
)

const defaultBufferCap = 1000

type Entry struct {
	Time    time.Time
	Level   string
	Message string
}

type ringBuffer struct {
	mu      sync.RWMutex
	entries []Entry
	cap     int
	start   int
	count   int
}

var globalBuffer = newRingBuffer(defaultBufferCap)

func newRingBuffer(capacity int) *ringBuffer {
	if capacity <= 0 {
		capacity = defaultBufferCap
	}
	return &ringBuffer{cap: capacity}
}

func (r *ringBuffer) add(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count < r.cap {
		r.entries = append(r.entries, e)
		r.count++
		return
	}
	r.entries[r.start] = e
	r.start = (r.start + 1) % r.cap
}

func (r *ringBuffer) entryAt(i int) Entry {
	if len(r.entries) < r.cap {
		return r.entries[i]
	}
	return r.entries[(r.start+i)%r.cap]
}

func (r *ringBuffer) snapshot(limit int) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := r.count
	if n == 0 {
		return nil
	}
	if limit <= 0 || limit > n {
		limit = n
	}
	skip := n - limit
	out := make([]Entry, limit)
	for i := 0; i < limit; i++ {
		out[i] = r.entryAt(skip + i)
	}
	return out
}

func Snapshot(limit int) []Entry {
	return globalBuffer.snapshot(limit)
}
