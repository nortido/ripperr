package tracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"trackerr/internal/helper"
	"trackerr/internal/helper/breakeralerts"
	"trackerr/internal/model/tracker"
	"trackerr/internal/proxy"
	"trackerr/internal/system/logger"
	"trackerr/internal/tracker/client"
	"trackerr/internal/tracker/client/kinozal"
	"trackerr/internal/tracker/client/nnm"
	"trackerr/internal/tracker/client/rutor"
	"trackerr/internal/tracker/client/rutracker"
	"trackerr/internal/tracker/client/teamhd"
)

const TTL = time.Hour

const maxStaleUses = 3

const (
	RutrackerID = 1
	KinozalID   = 2
	RutorID     = 3
	TeamhdID    = 5
	NnmID       = 6
)

type db interface {
	GetTracker(ctx context.Context, trackerID int) (res *tracker.Tracker, err error)
	SaveTracker(ctx context.Context, source *tracker.Tracker) (err error)
}

type CachedTracker struct {
	client.IClient
	Created time.Time
}

type service struct {
	px                 proxy.IClient
	db                 db
	mx                 sync.RWMutex
	flareMx            sync.Mutex
	flareTimeoutCounts map[int]int
	staleUseCounts     map[int]int
	cacheLocks         helper.KeyedMutex
	hc                 *http.Client
	downloadHC         helper.HTTPDoer
	clientIndex        map[int]*CachedTracker
	handlerRegistry    map[int]client.ITracker
	cbCfg              *helper.CircuitBreakerConfig
	alerts             *breakeralerts.Registry
	domainAlive        domainAliveFunc
}

func (s *service) GetID(link string) (id int) {
	for i, tr := range s.handlerRegistry {
		if tr.Detect(link) {
			return i
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
	ct, err := s.getCached(ctx, ttid)
	if err != nil {
		return
	}
	buf, err = s.readResponseBody(ctx, ttid, ct, link, download)
	if err == nil || !download {
		return
	}
	name := s.handlerRegistry[ttid].GetName()
	logger.Warn(fmt.Sprintf("%s: download failed, relogin: %s", name, err))
	if ct, err = s.relogin(ctx, ttid); err != nil {
		return
	}
	return s.readResponseBody(ctx, ttid, ct, link, download)
}

func (s *service) readResponseBody(ctx context.Context, ttid int, ct *CachedTracker, link string, download bool) (buf *bytes.Buffer, err error) {
	res, err := s.fetchResponse(ctx, ttid, ct, link, download)
	if err != nil {
		return
	}
	defer func() { _ = res.Body.Close() }()

	limit := int64(client.MaxPageResponse)
	if download {
		limit = client.MaxDownloadResponse
	}
	var b bytes.Buffer
	if err = client.ReadLimited(&b, res.Body, limit); err != nil {
		return
	}
	if download {
		if err = validateDownloadBody(res, &b); err != nil {
			return
		}
	}
	buf = &b
	return
}

func (s *service) fetchResponse(ctx context.Context, ttid int, ct *CachedTracker, link string, download bool) (res *http.Response, err error) {
	if res, err = ct.GetResponse(ctx, link, download); err != nil {
		if s.shouldReloginAfterFlare(ttid, err) {
			name := s.handlerRegistry[ttid].GetName()
			logger.Warn(fmt.Sprintf("%s: flare timeout limit reached, relogin", name))
			if ct, err = s.relogin(ctx, ttid); err == nil {
				res, err = ct.GetResponse(ctx, link, download)
			}
		}
		if err != nil {
			return
		}
	}
	s.resetFlareTimeouts(ttid)
	return
}

func validateDownloadBody(res *http.Response, buf *bytes.Buffer) (err error) {
	status, contentType := 0, ""
	if res != nil {
		status = res.StatusCode
		contentType = res.Header.Get("Content-Type")
	}
	if buf.Len() < 50 {
		err = fmt.Errorf("no file data (status=%d, type=%s, bytes=%d)", status, contentType, buf.Len())
		return
	}
	sample := buf.Bytes()
	if len(sample) > 512 {
		sample = sample[:512]
	}
	if bytes.Contains(sample, []byte("<html")) {
		err = fmt.Errorf("unexpected html response on download (status=%d, type=%s)", status, contentType)
	}
	return
}

func (s *service) BuildCache(ctx context.Context) (err error) {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		errs    []error
		success int
	)

	wg.Add(len(s.handlerRegistry))
	for i := range s.handlerRegistry {
		go func(i int) {
			defer wg.Done()
			s.destroyFlareSession(i)
			if e := s.cache(ctx, i, false); e != nil {
				if errors.Is(e, context.Canceled) {
					return
				}
				name := s.handlerRegistry[i].GetName()
				logger.Error(fmt.Sprintf("%s: %s", name, e))
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", name, e))
				mu.Unlock()
				return
			}
			mu.Lock()
			success++
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	if success == 0 && len(s.handlerRegistry) > 0 {
		err = errors.Join(errs...)
	}
	return
}

func (s *service) GetCachedClient(ctx context.Context, id int) (t client.IClient, err error) {
	return s.getCached(ctx, id)
}

func (s *service) getCached(ctx context.Context, id int) (ct *CachedTracker, err error) {
	var (
		ok         bool
		forceLogin bool
		prev       *CachedTracker
	)

	s.mx.RLock()
	ct, ok = s.clientIndex[id]
	if ok && time.Since(ct.Created) < TTL {
		if ct.IClient == nil {
			s.mx.RUnlock()
			err = fmt.Errorf("tracker http error")
			return
		}
		s.mx.RUnlock()
		return
	}
	prev = ct
	forceLogin = ok
	s.mx.RUnlock()

	if forceLogin {
		s.destroyFlareSession(id)
	}

	if err = s.cache(ctx, id, forceLogin); err != nil {
		return s.staleOnError(id, prev, s.handlerRegistry[id].GetName(), err)
	}
	s.resetStaleUseCount(id)
	s.mx.RLock()
	ct = s.clientIndex[id]
	s.mx.RUnlock()

	return
}

func (s *service) staleOnError(id int, prev *CachedTracker, name string, err error) (ct *CachedTracker, out error) {
	if prev == nil || prev.IClient == nil {
		return nil, err
	}
	s.flareMx.Lock()
	s.staleUseCounts[id]++
	count := s.staleUseCounts[id]
	s.flareMx.Unlock()
	if count > maxStaleUses {
		return nil, err
	}
	logger.Warn(fmt.Sprintf("%s: using stale cache: %s", name, err))
	return prev, nil
}

func (s *service) resetStaleUseCount(id int) {
	s.flareMx.Lock()
	delete(s.staleUseCounts, id)
	s.flareMx.Unlock()
}

func (s *service) destroyFlareSession(id int) {
	tr, ok := s.handlerRegistry[id]
	if !ok {
		return
	}
	p, ok := tr.(client.FlareSessionProvider)
	if !ok {
		return
	}
	sessionID := p.FlareSession()
	if sessionID == "" {
		return
	}
	if err := s.px.DestroyFlareSession(sessionID); err != nil {
		logger.Warn(fmt.Sprintf("%s: flare session destroy: %s", tr.GetName(), err))
	}
}

// DestroyFlareSessions removes flare sessions of all trackers; called on shutdown
// so FlareSolverr is not left with orphaned browser sessions.
func (s *service) DestroyFlareSessions() {
	for id := range s.handlerRegistry {
		s.destroyFlareSession(id)
	}
}

func (s *service) shouldReloginAfterFlare(ttid int, err error) bool {
	if !helper.IsFlareTimeout(err) || s.cbCfg == nil || s.cbCfg.MaxFailures <= 0 {
		return false
	}
	s.flareMx.Lock()
	defer s.flareMx.Unlock()
	s.flareTimeoutCounts[ttid]++
	return s.flareTimeoutCounts[ttid] >= s.cbCfg.MaxFailures
}

func (s *service) resetFlareTimeouts(ttid int) {
	s.flareMx.Lock()
	delete(s.flareTimeoutCounts, ttid)
	s.flareMx.Unlock()
}

func (s *service) relogin(ctx context.Context, ttid int) (ct *CachedTracker, err error) {
	var prev *CachedTracker

	s.resetFlareTimeouts(ttid)
	s.destroyFlareSession(ttid)

	s.mx.RLock()
	prev = s.clientIndex[ttid]
	s.mx.RUnlock()

	if err = s.cache(ctx, ttid, true); err != nil {
		return s.staleOnError(ttid, prev, s.handlerRegistry[ttid].GetName(), err)
	}
	s.resetStaleUseCount(ttid)
	s.mx.RLock()
	ct = s.clientIndex[ttid]
	s.mx.RUnlock()
	return
}

func (s *service) cache(ctx context.Context, tid int, forceLogin bool) (err error) {
	var (
		ok bool
		tr client.ITracker
		tc client.IClient
	)
	mu := s.cacheLocks.Mutex(tid)
	mu.Lock()
	defer mu.Unlock()
	if tr, ok = s.handlerRegistry[tid]; !ok {
		s.mx.Lock()
		s.clientIndex[tid] = &CachedTracker{Created: time.Now()}
		s.mx.Unlock()
		err = fmt.Errorf("tracker not exists")
		return
	}
	active := trackerActiveDomain(tr)
	if active == "" {
		active = selectActiveDomain(ctx, tr.GetDomain(), trackerMirrors(tr), s.domainAlive)
	}
	if active != tr.GetDomain() {
		logger.Info(fmt.Sprintf("tracker %s: using mirror %s", tr.GetName(), active))
		tr = withActiveDomain(tr, active)
	}
	if s.alerts != nil {
		s.alerts.Close(tr.GetName())
	}
	tc = client.New(s.hc, s.downloadHC, tr, s.px, s.newBreaker(tr.GetName()))
	if err = s.store(ctx, tid, tc, forceLogin); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		if forceLogin {
			logger.Warn(fmt.Sprintf("%s cache refresh failed: %s", tr.GetName(), err))
		} else {
			logger.Error(fmt.Sprintf("%s store error: %s", tr.GetName(), err))
		}
		return
	}
	s.mx.Lock()
	s.clientIndex[tid] = &CachedTracker{IClient: tc, Created: time.Now()}
	s.mx.Unlock()
	return
}

func (s *service) store(ctx context.Context, tid int, tc client.IClient, forceLogin bool) (err error) {
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

func New(hc *http.Client, downloadHC helper.HTTPDoer, px proxy.IClient, db db, cbCfg *helper.CircuitBreakerConfig, conf *tracker.TrackerConfig, alerts *breakeralerts.Registry) *service {
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
		hc:                 hc,
		downloadHC:         downloadHC,
		px:                 px,
		db:                 db,
		cbCfg:              cbCfg,
		alerts:             alerts,
		handlerRegistry:    handlerRegistry,
		clientIndex:        make(map[int]*CachedTracker),
		flareTimeoutCounts: make(map[int]int),
		staleUseCounts:     make(map[int]int),
		domainAlive:        defaultDomainAlive,
	}
}

func (s *service) newBreaker(name string) *helper.CircuitBreaker {
	cb := helper.NewCircuitBreaker(s.cbCfg)
	cb.TrackerName = name
	cb.Alerts = s.alerts
	return cb
}
