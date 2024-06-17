package tracker

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"torr/internal/helper"
	"torr/internal/model/proxy"
	"torr/internal/tracker/client"
	"torr/internal/tracker/client/kinozal"
	"torr/internal/tracker/client/rutor"
	"torr/internal/tracker/client/rutracker"
	"torr/internal/tracker/client/teamhd"
)

const (
	TTL = 30 * 24 * time.Hour

	RutrackerID = iota
	KinozalID
	RutorID
	TeamhdID
)

var (
	index = map[int]client.ITracker{
		RutrackerID: rutracker.New(),
		KinozalID:   kinozal.New(),
		RutorID:     rutor.New(),
		TeamhdID:    teamhd.New(),
	}
)

type IHttpClient interface {
	client.ITracker
	InitCookies(ctx context.Context) (err error)
	GetResponse(ctx context.Context, link string) (res *http.Response, err error)
}

type IProxy interface {
	Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (resp *http.Response, err error)
}

type IService interface {
	CacheAll(ctx context.Context) (err error)
	ClientByLink(ctx context.Context, link string) (tc IHttpClient, err error)
	Response(ctx context.Context, link string, tc IHttpClient) (buf *bytes.Buffer, err error)
}

type CachedItem struct {
	IHttpClient
	Created time.Time
}

type service struct {
	hc      *http.Client
	px      IProxy
	mx      sync.RWMutex
	clients map[int]*CachedItem
	cbcfg   *helper.CircuitBreakerConfig
}

func (s *service) ClientByLink(ctx context.Context, link string) (tc IHttpClient, err error) {
	var (
		id int
	)
	if id, err = s.getClientID(ctx, link); err != nil {
		return
	}
	tc, err = s.getCachedClient(ctx, id)

	return
}

func (s *service) Response(ctx context.Context, link string, tc IHttpClient) (buf *bytes.Buffer, err error) {
	var (
		b   = bytes.Buffer{}
		res *http.Response
	)
	if tc == nil {
		tc, err = s.ClientByLink(ctx, link)
	}
	if res, err = tc.GetResponse(ctx, link); err != nil {
		return
	}
	defer res.Body.Close()

	if _, err = b.ReadFrom(res.Body); err != nil {
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
		if ct.IHttpClient == nil {
			continue
		}
		if ct.IHttpClient.Detect(url) {
			id = i
			return
		}
	}
	err = fmt.Errorf("tracker not found")

	return
}

func (s *service) getCachedClient(ctx context.Context, id int) (t IHttpClient, err error) {
	var (
		ok bool
		ct *CachedItem
	)
	if ct, ok = s.clients[id]; ok && time.Since(ct.Created) < TTL {
		if ct.IHttpClient == nil {
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
		tr client.ITracker
		tc IHttpClient
	)

	// TODO: store login cookies in DB
	if tr, ok = index[tid]; !ok {
		s.mx.RLock()
		s.clients[tid] = &CachedItem{Created: time.Now()}
		s.mx.RUnlock()
		err = fmt.Errorf("tracker not exists")
		return
	}
	tc = client.New(s.hc, tr, s.px, helper.NewCircuitBreaker(s.cbcfg))
	fmt.Println("trying to cache ", tr.Name())
	if err = tc.InitCookies(ctx); err != nil {
		tc = nil
	}
	s.mx.RLock()
	s.clients[tid] = &CachedItem{IHttpClient: tc, Created: time.Now()}
	s.mx.RUnlock()
	if tc != nil {
		fmt.Println("tracker cached: ", tr.Name())
	}

	return
}

func New(hc *http.Client, px IProxy, cbcfg *helper.CircuitBreakerConfig) IService {
	return &service{
		hc:      hc,
		px:      px,
		clients: make(map[int]*CachedItem),
		cbcfg:   cbcfg,
	}
}
