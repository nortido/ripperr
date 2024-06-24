package tracker

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"trackerr/internal/helper"
	"trackerr/internal/model/proxy"
	"trackerr/internal/model/tracker"
	"trackerr/internal/tracker/client"
	"trackerr/internal/tracker/client/kinozal"
	"trackerr/internal/tracker/client/nnm"
	"trackerr/internal/tracker/client/rutor"
	"trackerr/internal/tracker/client/rutracker"
	"trackerr/internal/tracker/client/teamhd"
)

const (
	TTL = 30 * 24 * time.Hour

	RutrackerID = iota
	KinozalID
	RutorID
	TeamhdID
	NnmID
)

var (
	index = map[int]client.ITracker{
		RutrackerID: rutracker.New(),
		KinozalID:   kinozal.New(),
		RutorID:     rutor.New(),
		TeamhdID:    teamhd.New(),
		NnmID:       nnm.New(),
	}
)

type db interface {
	GetTracker(ctx context.Context, trackerID int) (res *tracker.Tracker, err error)
	SaveTracker(ctx context.Context, source *tracker.Tracker) (err error)
}

type IHttpClient interface {
	client.ITracker
	Cookies() (res []*http.Cookie)
	InitClient(ctx context.Context, payload *tracker.Tracker) (err error)
	GetResponse(ctx context.Context, link string, download bool) (res *http.Response, err error)
}

type px interface {
	Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (resp *http.Response, err error)
}

type IService interface {
	CacheAll(ctx context.Context) (err error)
	ClientByLink(ctx context.Context, link string) (tc IHttpClient, err error)
	Response(ctx context.Context, link string, tc IHttpClient, download bool) (buf *bytes.Buffer, err error)
}

type CachedItem struct {
	IHttpClient
	Created time.Time
}

type service struct {
	hc      *http.Client
	px      px
	mx      sync.RWMutex
	clients map[int]*CachedItem
	cbCfg   *helper.CircuitBreakerConfig
	db      db
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

func (s *service) Response(ctx context.Context, link string, tc IHttpClient, download bool) (buf *bytes.Buffer, err error) {
	var (
		b   = bytes.Buffer{}
		res *http.Response
	)
	if tc == nil {
		tc, err = s.ClientByLink(ctx, link)
	}
	if res, err = tc.GetResponse(ctx, link, download); err != nil {
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
	var (
		wg sync.WaitGroup
	)
	wg.Add(len(index))
	for i := range index {
		go func(i int) {
			var e error
			defer wg.Done()
			if e = s.cache(ctx, i); e != nil {
				err = e
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
	// TODO: add entry without tracker to blacklist
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
	if err = s.cache(ctx, id); err != nil {
		return
	}
	t = s.clients[id]
	return
}

func (s *service) cache(ctx context.Context, tid int) (err error) {
	var (
		ok bool
		tr client.ITracker
		tc IHttpClient
	)

	if tr, ok = index[tid]; !ok {
		s.mx.Lock()
		s.clients[tid] = &CachedItem{Created: time.Now()}
		s.mx.Unlock()
		err = fmt.Errorf("tracker not exists")
		return
	}
	tc = client.New(s.hc, tr, s.px, helper.NewCircuitBreaker(s.cbCfg))
	fmt.Println("trying to cache ", tr.Name())
	if err = s.store(ctx, tid, tc); err != nil {
		return
	}
	s.mx.Lock()
	s.clients[tid] = &CachedItem{IHttpClient: tc, Created: time.Now()}
	s.mx.Unlock()
	if tc != nil {
		fmt.Println("tracker cached: ", tr.Name())
	} else {
		fmt.Println("tracker caching failed: ", tr.Name())
	}

	return
}

func (s *service) store(ctx context.Context, tid int, tc IHttpClient) (err error) {
	var (
		creds *url.Values
		attrs *tracker.Tracker
	)
	if attrs, err = s.db.GetTracker(ctx, tid); err != nil && err != pgx.ErrNoRows {
		return
	}
	if err = tc.InitClient(ctx, attrs); err != nil {
		tc = nil
		return
	}
	if attrs != nil {
		return
	}
	_, creds, _ = tc.LoginData()
	attrs = &tracker.Tracker{
		TypeID:      tid,
		Name:        tc.Name(),
		Cookies:     tc.Cookies(),
		Credentials: creds.Encode(),
	}
	if err = s.db.SaveTracker(ctx, attrs); err != nil {
		return
	}
	return
}

func New(hc *http.Client, px px, db db, cbCfg *helper.CircuitBreakerConfig) IService {
	return &service{
		hc:      hc,
		px:      px,
		db:      db,
		cbCfg:   cbCfg,
		clients: make(map[int]*CachedItem),
	}
}
