package tracker

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"trackerr/internal/helper"
	"trackerr/internal/model/proxy"
	"trackerr/internal/model/tracker"
	"trackerr/internal/system/logger"
	"trackerr/internal/tracker/client"
	"trackerr/internal/tracker/client/kinozal"
	"trackerr/internal/tracker/client/nnm"
	"trackerr/internal/tracker/client/rutor"
	"trackerr/internal/tracker/client/rutracker"
	"trackerr/internal/tracker/client/teamhd"
)

const (
	TTL = time.Hour

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

type px interface {
	Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (resp *http.Response, err error)
}

type IHttpClient interface {
	client.ITracker
	Cookies() (res []*http.Cookie)
	InitClient(ctx context.Context, payload *tracker.Tracker) (err error)
	GetResponse(ctx context.Context, link string, download bool) (res *http.Response, err error)
}

type IService interface {
	BuildCache(ctx context.Context) (err error)
	CachedList() (res map[int]client.ITracker)
	GetID(url string) (id int)
	GetHourLimit(ttid int) (limit int)
	GetCachedClient(ctx context.Context, ttid int) (tc IHttpClient, err error)
	Response(ctx context.Context, ttid int, link string, download bool) (buf *bytes.Buffer, err error)
}

type CachedTracker struct {
	IHttpClient
	Created time.Time
}

type service struct {
	px      px
	db      db
	mx      sync.RWMutex
	hc      *http.Client
	clients map[int]*CachedTracker
	cbCfg   *helper.CircuitBreakerConfig
}

func (s *service) GetID(link string) (id int) {
	var (
		i  int
		ct *CachedTracker
	)
	for i, ct = range s.clients {
		if ct.IHttpClient == nil {
			continue
		}
		if ct.IHttpClient.Detect(link) {
			id = i
			return
		}
	}
	return
}

func (s *service) GetHourLimit(ttid int) (limit int) {
	return index[ttid].HourLimit()
}

func (s *service) CachedList() (res map[int]client.ITracker) {
	return index
}

func (s *service) Response(ctx context.Context, ttid int, link string, download bool) (buf *bytes.Buffer, err error) {
	var (
		res *http.Response
		ct  *CachedTracker
		b   = bytes.Buffer{}
	)
	if ct, err = s.getCached(ctx, ttid); err != nil {
		return
	}
	if res, err = ct.GetResponse(ctx, link, download); err != nil {
		return
	}
	defer res.Body.Close()

	if _, err = b.ReadFrom(res.Body); err != nil {
		return
	}
	buf = &b

	return
}

func (s *service) BuildCache(ctx context.Context) (err error) {
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

func (s *service) GetCachedClient(ctx context.Context, id int) (t IHttpClient, err error) {
	return s.getCached(ctx, id)
}

func (s *service) getCached(ctx context.Context, id int) (ct *CachedTracker, err error) {
	var ok bool

	if ct, ok = s.clients[id]; ok && time.Since(ct.Created) < TTL {
		if ct.IHttpClient == nil {
			err = fmt.Errorf("tracker http error")
			return
		}
		return
	}
	if err = s.cache(ctx, id); err != nil {
		return
	}
	ct = s.clients[id]

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
		s.clients[tid] = &CachedTracker{Created: time.Now()}
		s.mx.Unlock()
		err = fmt.Errorf("tracker not exists")
		return
	}
	tc = client.New(s.hc, tr, s.px, helper.NewCircuitBreaker(s.cbCfg))
	logger.Info(fmt.Sprint("trying to cache ", tr.Name()))
	if err = s.store(ctx, tid, tc); err != nil {
		return
	}
	s.mx.Lock()
	s.clients[tid] = &CachedTracker{IHttpClient: tc, Created: time.Now()}
	s.mx.Unlock()

	if tc != nil {
		logger.Info(fmt.Sprint("tracker cached: ", tr.Name()))
	} else {
		logger.Error(fmt.Sprint("tracker caching failed: ", tr.Name()))
	}
	return
}

func (s *service) store(ctx context.Context, tid int, tc IHttpClient) (err error) {
	var (
		link  string
		creds *url.Values
		attrs *tracker.Tracker
	)
	if attrs, err = s.db.GetTracker(ctx, tid); err != nil {
		return
	}
	if err = tc.InitClient(ctx, attrs); err != nil {
		tc = nil
		return
	}
	if attrs != nil {
		return
	}
	link, creds, _ = tc.LoginData()
	attrs = &tracker.Tracker{
		TypeID:      tid,
		Name:        tc.Name(),
		Host:        tc.Host(),
		LoginURL:    link,
		DownloadURL: tc.DownloadLink(""),
		Publicity:   tc.Publicity(),
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
		clients: make(map[int]*CachedTracker),
	}
}
