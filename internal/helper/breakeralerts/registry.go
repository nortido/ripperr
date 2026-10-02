package breakeralerts

import (
	"sort"
	"sync"
	"time"
)

type Incident struct {
	Tracker   string
	Since     time.Time
	LastError string
}

type Registry struct {
	mu        sync.RWMutex
	incidents map[string]Incident
	hook      func(open bool, tracker, errMsg string)
}

func New() *Registry {
	return &Registry{incidents: make(map[string]Incident)}
}

func (r *Registry) SetHook(hook func(open bool, tracker, errMsg string)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.hook = hook
	r.mu.Unlock()
}

func (r *Registry) Open(tracker, errMsg string) {
	if r == nil || tracker == "" {
		return
	}
	r.mu.Lock()
	inc, existed := r.incidents[tracker]
	if !existed {
		inc = Incident{Tracker: tracker, Since: time.Now()}
	}
	inc.LastError = errMsg
	r.incidents[tracker] = inc
	hook := r.hook
	r.mu.Unlock()
	// хук это notify.Notify, он не блокирует, поэтому зовем его на месте после Unlock
	if !existed && hook != nil {
		hook(true, tracker, errMsg)
	}
}

func (r *Registry) Close(tracker string) {
	if r == nil || tracker == "" {
		return
	}
	r.mu.Lock()
	_, existed := r.incidents[tracker]
	delete(r.incidents, tracker)
	hook := r.hook
	r.mu.Unlock()
	if existed && hook != nil {
		hook(false, tracker, "")
	}
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
