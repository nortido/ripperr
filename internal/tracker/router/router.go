package router

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"time"
	"torr/internal/helper"
	"torr/internal/model"
	"torr/internal/tracker/client/kinozal"
	"torr/internal/tracker/client/rutracker"
)

type Tracker interface {
	Login(ctx context.Context) (err error)
	Request(ctx context.Context, e *model.Entry) (res *http.Response, err error)
}

const (
	TTL         = 10 * time.Minute
	RutrackerID = iota
	KinozalID
)

var (
	trackersFns = map[int]func(hc *http.Client) Tracker{
		RutrackerID: func(hc *http.Client) Tracker {
			return rutracker.New(hc)
		},
		KinozalID: func(hc *http.Client) Tracker {
			return kinozal.New(hc)
		},
		},
	}
)

type Router interface {
	GetTracker(ctx context.Context, e *model.Entry) (t Tracker, err error)
	CacheTrackers(ctx context.Context) (err error)
}

type CachedItem struct {
	Tracker
	Created time.Time
}

type router struct {
	hc       *http.Client
	mx       sync.RWMutex
	trackers map[int]*CachedItem
	cb       *helper.CircuitBreaker
}

func (r *router) CacheTrackers(ctx context.Context) (err error) {
	var wg sync.WaitGroup
	for _, i := range trackerIDs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err = r.store(ctx, i); err != nil {
				return
			}
		}(i)
	}
	wg.Wait()
	return
}

func (r *router) GetTracker(ctx context.Context, e *model.Entry) (t Tracker, err error) {
	var (
		id int
		ok bool
		ct *CachedItem
	)
	switch {
	//TODO: add func to check url
	case helper.Substr(e.URL, 0, len(rutracker.TopicURL)) == rutracker.TopicURL:
		id = RutrackerID
	case helper.Substr(e.URL, 0, len(kinozal.TopicURL)) == kinozal.TopicURL:
		id = KinozalID
	default:
		err = fmt.Errorf("tracker not found")
		return
	}
	if ct, ok = r.trackers[id]; ok && time.Since(ct.Created) < TTL {
		if ct.Tracker == nil {
			err = fmt.Errorf("tracker http error")
			return
		}
		t = ct
		return
	}
	if err = r.store(ctx, id); err != nil {
		return
	}
	t = r.trackers[id]
	return
}

func (r *router) store(ctx context.Context, tid int) (err error) {
	var (
		tr Tracker
	)

	tr = trackersFns[tid](r.hc)
	fmt.Println("trying to cache ", reflect.TypeOf(tr).String())
	if err = tr.Login(ctx); err != nil {
		tr = nil
	}
	r.mx.RLock()
	r.trackers[tid] = &CachedItem{Tracker: tr, Created: time.Now()}
	r.mx.RUnlock()
	if tr != nil {
		fmt.Println("tracker cached: ", reflect.TypeOf(tr).String())
	}

	return
}

func New() Router {
	return &router{
		hc:       &http.Client{},
		trackers: make(map[int]*CachedItem),
		cb:       helper.NewCircuitBreaker(2, 1*time.Minute),
	}
}
