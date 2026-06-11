package tracker

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
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

type db interface {
	GetTracker(ctx context.Context, trackerID int) (res *tracker.Tracker, err error)
	SaveTracker(ctx context.Context, source *tracker.Tracker) (err error)
}

type px interface {
	Send(r *http.Request, opts *proxy.Options, proxyType proxy.ProxyType) (resp *http.Response, err error)
	DestroyFlareSession(sessionID string) error
}

type IHttpClient interface {
	client.ITracker
	Cookies() (res []*http.Cookie)
	InitClient(ctx context.Context, payload *tracker.Tracker, forceLogin bool) (err error)
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
	px              px
	db              db
	mx              sync.RWMutex
	cacheMx         sync.Map
	hc              *http.Client
	clientIndex     map[int]*CachedTracker
	handlerRegistry map[int]client.ITracker
	cbCfg           *helper.CircuitBreakerConfig
}

func (s *service) GetID(link string) (id int) {
	var (
		i int
	)
	for i = range s.clientIndex {
		if s.clientIndex[i].IHttpClient == nil {
			continue
		}
		if s.clientIndex[i].IHttpClient.Detect(link) {
			id = i
			return
		}
	}
	return
}

func (s *service) GetHourLimit(ttid int) (limit int) {
	return s.handlerRegistry[ttid].GetHourLimit()
}

func (s *service) CachedList() (res map[int]client.ITracker) {
	return s.handlerRegistry
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
	var wg sync.WaitGroup

	wg.Add(len(s.handlerRegistry))
	for i := range s.handlerRegistry {
		go func(i int) {
			defer wg.Done()
			if e := s.cache(ctx, i, false); e != nil {
				logger.Error(fmt.Sprintf("%s: %s", s.handlerRegistry[i].GetName(), e))
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
	var (
		ok         bool
		forceLogin bool
		prev       *CachedTracker
	)

	if ct, ok = s.clientIndex[id]; ok && time.Since(ct.Created) < TTL {
		if ct.IHttpClient == nil {
			err = fmt.Errorf("tracker http error")
			return
		}
		return
	}
	prev = ct
	forceLogin = ok
	if err = s.cache(ctx, id, forceLogin); err != nil {
		if prev != nil && prev.IHttpClient != nil {
			logger.Warn(fmt.Sprintf("%s: refresh failed, using stale cache: %s", s.handlerRegistry[id].GetName(), err))
			prev.Created = time.Now()
			return prev, nil
		}
		return
	}
	ct = s.clientIndex[id]

	return
}

func (s *service) cacheLock(tid int) *sync.Mutex {
	v, _ := s.cacheMx.LoadOrStore(tid, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *service) cache(ctx context.Context, tid int, forceLogin bool) (err error) {
	var (
		ok bool
		tr client.ITracker
		tc IHttpClient
	)
	mu := s.cacheLock(tid)
	mu.Lock()
	defer mu.Unlock()
	if tr, ok = s.handlerRegistry[tid]; !ok {
		s.mx.Lock()
		s.clientIndex[tid] = &CachedTracker{Created: time.Now()}
		s.mx.Unlock()
		err = fmt.Errorf("tracker not exists")
		return
	}
	tc = client.New(s.hc, tr, s.px, helper.NewCircuitBreaker(s.cbCfg))
	// TODO: add debug env
	//logger.Info(fmt.Sprint("trying to cache ", tr.GetName()))
	if err = s.store(ctx, tid, tc, forceLogin); err != nil {
		if forceLogin {
			logger.Warn(fmt.Sprintf("%s cache refresh failed: %s", tr.GetName(), err))
		} else {
			logger.Error(fmt.Sprintf("%s store error: %s", tr.GetName(), err))
		}
		return
	}
	s.mx.Lock()
	s.clientIndex[tid] = &CachedTracker{IHttpClient: tc, Created: time.Now()}
	s.mx.Unlock()

	if tc != nil {
		//logger.Info(fmt.Sprint("tracker cached: ", tr.GetName()))
	} else {
		logger.Error(fmt.Sprint("tracker caching failed: ", tr.GetName()))
	}
	return
}

func (s *service) store(ctx context.Context, tid int, tc IHttpClient, forceLogin bool) (err error) {
	var (
		attrs      *tracker.Tracker
		hadCookies bool
	)

	if attrs, err = s.db.GetTracker(ctx, tid); err != nil {
		return
	}
	hadCookies = attrs.Exists && len(attrs.Cookies) > 0
	if err = tc.InitClient(ctx, attrs, forceLogin); err != nil {
		tc = nil
		return
	}
	if attrs.Exists {
		if len(tc.Cookies()) > 0 && (forceLogin || !hadCookies || helper.CookiesString(attrs.Cookies) != helper.CookiesString(tc.Cookies())) {
			attrs.Cookies = tc.Cookies()
			attrs.Exists = true
			if err = s.db.SaveTracker(ctx, attrs); err != nil {
				return
			}
		}
		return
	}
	attrs = &tracker.Tracker{
		TypeID:      tid,
		Name:        tc.GetName(),
		Domain:      tc.GetDomain(),
		LoginURL:    tc.LoginLink(),
		DownloadURL: tc.DownloadLink(""),
		Publicity:   tc.GetPublicity(),
		Cookies:     tc.Cookies(),
		Credentials: tc.GetCredentials(),
	}
	if err = s.db.SaveTracker(ctx, attrs); err != nil {
		return
	}
	return
}

func New(hc *http.Client, px px, db db, cbCfg *helper.CircuitBreakerConfig, conf *tracker.TrackerConfig) IService {
	handlerRegistry := make(map[int]client.ITracker)
	if conf.Rutracker.Enabled {
		handlerRegistry[RutrackerID] = rutracker.New(conf.Rutracker)
	}
	if conf.Kinozal.Enabled {
		handlerRegistry[KinozalID] = kinozal.New(conf.Kinozal)
	}
	if conf.Rutor.Enabled {
		handlerRegistry[RutorID] = rutor.New(conf.Rutor)
	}
	}
	if conf.TeamHD.Enabled {
		handlerRegistry[TeamhdID] = teamhd.New(conf.TeamHD)
	}
	if conf.NNM.Enabled {
		handlerRegistry[NnmID] = nnm.New(conf.NNM)
	}
	return &service{
		hc:              hc,
		px:              px,
		db:              db,
		cbCfg:           cbCfg,
		handlerRegistry: handlerRegistry,
		clientIndex:     make(map[int]*CachedTracker),
	}
}
