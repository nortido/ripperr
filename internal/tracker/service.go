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
	"torr/internal/tracker/client/teamhd"
)

const (
	TTL = 10 * time.Minute

	RutrackerID = iota
	KinozalID
	RutorID
	TeamhdID
)

var (
	index = map[int]client.Tracker{
		RutrackerID: rutracker.New(),
		KinozalID:   kinozal.New(),
		RutorID:     rutor.New(),
		TeamhdID:    teamhd.New(),
	}
)

type HttpClient interface {
	Tracker() (tc client.Tracker)
	InitSession(ctx context.Context) (err error)
	GetResponse(ctx context.Context, link string, tryProxy bool) (res *http.Response, err error)
}

type Proxy interface {
	Send(r *http.Request, opts *model.ProxyRequestOptions) (resp *http.Response, err error)
}

type Service interface {
	CacheAll(ctx context.Context) (err error)
	Get(ctx context.Context, e *model.Entry) (buf *bytes.Buffer, err error)
	File(ctx context.Context, tc HttpClient, downloadLink string) (buf *bytes.Buffer, err error)
}

type CachedItem struct {
	HttpClient
	Created time.Time
}

type service struct {
	hc      *http.Client
	px      Proxy
	mx      sync.RWMutex
	clients map[int]*CachedItem
	cb      *helper.CircuitBreaker
}

func (s *service) Get(ctx context.Context, e *model.Entry) (buf *bytes.Buffer, err error) {
	var (
		id      int
		res     *http.Response
		tc      HttpClient
		current *time.Time
	)

	if id, err = s.getClientID(ctx, e.URL); err != nil {
		return
	}
	if tc, err = s.getCachedClient(ctx, id); err != nil {
		return
	}
	if res, err = tc.GetResponse(ctx, tc.Tracker().TopicLink(e.ID), false); err != nil {
		return
	}
	defer res.Body.Close()
	buf = &bytes.Buffer{}

	if _, err = buf.ReadFrom(res.Body); err != nil {
		return
	}
	if current = tc.Tracker().Datetime(buf, false); current == nil {
		err = fmt.Errorf("current time not found")
		return
	}
	// TODO: add check count of updates in DB
	if current.Unix()-e.Timestamp > 43200 {
		err = fmt.Errorf("file is not updated")
		return
	}
	buf, err = s.File(ctx, tc, tc.Tracker().DownloadLink(e.ID))

	return
}

func (s *service) File(ctx context.Context, tc HttpClient, downloadLink string) (buf *bytes.Buffer, err error) {
	var (
		b   = bytes.Buffer{}
		res *http.Response
	)

	if res, err = tc.GetResponse(ctx, downloadLink, false); err != nil {
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

func (s *service) CacheAll(ctx context.Context) (err error) {
	var wg sync.WaitGroup
	wg.Add(len(index))
	for i := range index {
		go func(i int) {
			defer wg.Done()
			if err = s.store(ctx, i); err != nil {
				return
			}
		}(i)
	}
	wg.Wait()
	return
}

func (s *service) getClientID(ctx context.Context, url string) (id int, err error) {
	var (
		i  int
		ct *CachedItem
	)
	for i, ct = range s.clients {
		if ct.HttpClient == nil {
			continue
		}
		if ct.HttpClient.Tracker().Detect(url) {
			id = i
			return
		}
	}
	err = fmt.Errorf("tracker not found")

	return
}

func (s *service) getCachedClient(ctx context.Context, id int) (t HttpClient, err error) {
	var (
		ok bool
		ct *CachedItem
	)
	if ct, ok = s.clients[id]; ok && time.Since(ct.Created) < TTL {
		if ct.HttpClient == nil {
			err = fmt.Errorf("tracker http error")
			return
		}
		t = ct
		return
	}
	if err = s.store(ctx, id); err != nil {
		return
	}
	t = s.clients[id]
	return
}

func (s *service) store(ctx context.Context, tid int) (err error) {
	var (
		ok bool
		tr client.Tracker
		tc HttpClient
	)

	if tr, ok = index[tid]; !ok {
		s.mx.RLock()
		s.clients[tid] = &CachedItem{Created: time.Now()}
		s.mx.RUnlock()
		err = fmt.Errorf("tracker not exists")
		return
	}
	tc = client.New(s.hc, tr, s.px)
	fmt.Println("trying to cache ", tr.Name())
	if err = tc.InitSession(ctx); err != nil {
		tc = nil
	}
	s.mx.RLock()
	s.clients[tid] = &CachedItem{HttpClient: tc, Created: time.Now()}
	s.mx.RUnlock()
	if tc != nil {
		fmt.Println("tracker cached: ", tr.Name())
	}

	return
}

func New(hc *http.Client, px Proxy) Service {
	return &service{
		hc:      hc,
		px:      px,
		clients: make(map[int]*CachedItem),
		cb:      helper.NewCircuitBreaker(2, 1*time.Minute),
	}
}
