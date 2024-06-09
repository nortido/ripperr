package tracker

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"torr/internal/helper"
	"torr/internal/model"
	"torr/internal/tracker/client"
	"torr/internal/tracker/client/kinozal"
	"torr/internal/tracker/client/rutor"
	"torr/internal/tracker/client/rutracker"
)

type Tracker interface {
	Header() (header *http.Header)
	Host() (host string)
	DownloadURL(topicID string) (url string)
	TopicURL(topicID string) (url string)
	Detect(url string) bool
	Name() (name string)
}

type HttpClient interface {
	Prepare(ctx context.Context) (err error)
	GetResponse(ctx context.Context, e *model.Entry) (res *http.Response, err error)
}

const (
	TTL = 10 * time.Minute

	RutrackerID = iota
	KinozalID
	RutorID
)

var (
	index = map[int]Tracker{
		RutrackerID: rutracker.New(),
		KinozalID:   kinozal.New(),
		RutorID:     rutor.New(),
	}
)

type Proxy interface {
	Get(r *http.Request) (resp *http.Response, err error)
}

type Service interface {
	CacheAll(ctx context.Context) (err error)
	GetData(ctx context.Context, e *model.Entry) (buf *bytes.Buffer, err error)
}

type CachedItem struct {
	HttpClient
	Tracker
	Created time.Time
}

type service struct {
	hc       *http.Client
	px       Proxy
	mx       sync.RWMutex
	trackers map[int]*CachedItem
	cb       *helper.CircuitBreaker
}

func (r *service) GetData(ctx context.Context, e *model.Entry) (buf *bytes.Buffer, err error) {
	var (
		id  int
		b   = bytes.Buffer{}
		res *http.Response
		tc  HttpClient
	)

	if id, err = r.getClientID(ctx, e.URL); err != nil {
		return
	}
	if tc, err = r.getCachedClient(ctx, id); err != nil {
		return
	}
	if res, err = tc.GetResponse(ctx, e); err != nil {
		return
	}
	if _, err = b.ReadFrom(res.Body); err != nil {
		return
	}
	if err = res.Body.Close(); err != nil {
		return
	}
	buf = &b

	return
}

func (r *service) CacheAll(ctx context.Context) (err error) {
	var wg sync.WaitGroup
	wg.Add(len(index))
	for i := range index {
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

func (r *service) getClientID(ctx context.Context, url string) (id int, err error) {
	for i, tr := range r.trackers {
		if tr.Detect(url) {
			id = i
			return
		}
	}
	err = fmt.Errorf("tracker not found")

	return
}

func (r *service) getCachedClient(ctx context.Context, id int) (t HttpClient, err error) {
	var (
		ok bool
		ct *CachedItem
	)
	if ct, ok = r.trackers[id]; ok && time.Since(ct.Created) < TTL {
		if ct.HttpClient == nil {
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

func (r *service) store(ctx context.Context, tid int) (err error) {
	var (
		ok bool
		tr Tracker
		tc HttpClient
	)

	if tr, ok = index[tid]; !ok {
		r.mx.RLock()
		r.trackers[tid] = &CachedItem{Created: time.Now()}
		r.mx.RUnlock()
		err = fmt.Errorf("tracker not exists")
		return
	}
	tc = client.New(r.hc, tr, r.px)
	fmt.Println("trying to cache ", tr.Name())
	if err = tc.Prepare(ctx); err != nil {
		tc = nil
	}
	r.mx.RLock()
	r.trackers[tid] = &CachedItem{HttpClient: tc, Tracker: tr, Created: time.Now()}
	r.mx.RUnlock()
	if tc != nil {
		fmt.Println("tracker cached: ", tr.Name())
	}

	return
}

func New(hc *http.Client, px Proxy) Service {
	return &service{
		hc:       hc,
		px:       px,
		trackers: make(map[int]*CachedItem),
		cb:       helper.NewCircuitBreaker(2, 1*time.Minute),
	}
}
