package breakeralerts

import (
	"sort"
	"sync"
	"time"
)

// Incident is an active circuit-breaker trip for one tracker.
type Incident struct {
	Tracker   string
	Since     time.Time
	LastError string
}

// Registry holds open breaker incidents; survives app reload.
type Registry struct {
	mu        sync.RWMutex
	incidents map[string]Incident
}

func New() *Registry {
	return &Registry{incidents: make(map[string]Incident)}
}

func (r *Registry) Open(tracker, errMsg string) {
	if r == nil || tracker == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	inc, exists := r.incidents[tracker]
	if !exists {
		inc = Incident{Tracker: tracker, Since: time.Now()}
	}
	inc.LastError = errMsg
	r.incidents[tracker] = inc
}

func (r *Registry) Close(tracker string) {
	if r == nil || tracker == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.incidents, tracker)
}

func (r *Registry) List() []Incident {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Incident, 0, len(r.incidents))
	for _, inc := range r.incidents {
		out = append(out, inc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tracker < out[j].Tracker })
	return out
}
