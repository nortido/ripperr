package tracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"ripperr/internal/helper"
	"ripperr/internal/helper/breakeralerts"
	"ripperr/internal/helper/tlsclient"
	"ripperr/internal/model/tracker"
	"ripperr/internal/proxy"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker/client"
	_ "ripperr/internal/tracker/registry/all"
)

const TTL = time.Hour

const maxStaleUses = 3

var loginMaxAge = 24 * time.Hour

const missLoginGap = time.Hour

type loginTimes struct {
	mu sync.Mutex
	at map[int]time.Time
}

func (l *loginTimes) get(id int) (t time.Time, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok = l.at[id]
	return
}

func (l *loginTimes) set(id int, t time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.at == nil {
		l.at = make(map[int]time.Time)
	}
	l.at[id] = t
}

func (l *loginTimes) del(id int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.at, id)
}

// время входа живет дольше сервиса: перезагрузка настроек не должна сбрасывать окно loginMaxAge
var processLogins = &loginTimes{}

var ErrMirrorUnconfirmed = errors.New("only an unconfirmed mirror answers")

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
	logins             *loginTimes
	loginsOnce         sync.Once
	handlerRegistry    map[int]client.ITracker
	cbCfg              *helper.CircuitBreakerConfig
	alerts             *breakeralerts.Registry
	domainAlive        domainProbe
	adopted            map[string]string
	rot                mirrorRotation
}

func (s *service) loginClock() *loginTimes {
	s.loginsOnce.Do(func() {
		if s.logins == nil {
			s.logins = &loginTimes{}
		}
	})
	return s.logins
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
	tr, ok := s.handlerRegistry[ttid]
	if !ok {
		return 0
	}
	return tr.GetHourLimit()
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
	if !download {
		return s.afterPage(ctx, ttid, ct, link, buf, err)
	}
	if err == nil {
		return
	}
	if !sessionSuspect(err) {
		return
	}
	name := s.handlerRegistry[ttid].GetName()
	logger.Warn(fmt.Sprintf("%s: download failed, relogin: %s", name, err))
	if ct, err = s.relogin(ctx, ttid); err != nil {
		return
	}
	return s.readResponseBody(ctx, ttid, ct, link, download)
}

func (s *service) ForceLoginNext(ttid int) {
	s.mx.Lock()
	defer s.mx.Unlock()
	ct, ok := s.clientIndex[ttid]
	// не чаще раза в час, как до повторного использования кук: легальный промах не должен плодить входы
	if last, seen := s.loginClock().get(ttid); !ok || ct.IClient == nil || (seen && time.Since(last) < missLoginGap) {
		return
	}
	s.loginClock().del(ttid)
	ct.Created = time.Time{}
}

func sessionSuspect(err error) bool {
	// сеть, таймаут, 404 и 5xx ничего не говорят о сессии: вход заново их не лечит
	if errors.Is(err, tlsclient.ErrRedirectRefused) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, helper.ErrCircuitOpen) || errors.Is(err, client.ErrTrackerUnavailable) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return false
	}
	var se *client.StatusError
	if errors.As(err, &se) && (se.Code == http.StatusNotFound || se.Code >= http.StatusInternalServerError) {
		return false
	}
	return true
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
			s.destroyFlareSession(ctx, i)
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

	// пауза главнее кеша и TTL: до ее конца в сеть не ходим, после нее домен выбираем заново
	host, resume := s.pausedHost(id)
	if host != "" && !resume {
		err = pauseError(host)
		return
	}

	s.mx.RLock()
	ct, ok = s.clientIndex[id]
	if !resume && ok && time.Since(ct.Created) < TTL {
		if ct.IClient == nil {
			s.mx.RUnlock()
			err = fmt.Errorf("%w: tracker http error", client.ErrTrackerUnavailable)
			return
		}
		s.mx.RUnlock()
		return
	}
	prev = ct
	// куки из БД живут до суток: почасовая пересборка входит заново только после паузы или спустя loginMaxAge
	last, seen := s.loginClock().get(id)
	forceLogin = ok && (resume || !seen || time.Since(last) >= loginMaxAge)
	s.mx.RUnlock()

	if forceLogin {
		s.destroyFlareSession(ctx, id)
	}

	return s.refresh(ctx, id, forceLogin, prev)
}

func (s *service) refresh(ctx context.Context, id int, forceLogin bool, prev *CachedTracker) (ct *CachedTracker, err error) {
	if err = s.cache(ctx, id, forceLogin); err != nil {
		// старый клиент сидит на мертвом хосте, отдавать его в паузу нельзя
		if errors.Is(err, ErrMirrorUnconfirmed) {
			return nil, err
		}
		err = fmt.Errorf("%w: %w", client.ErrTrackerUnavailable, err)
		name := fmt.Sprintf("%d", id)
		if tr, ok := s.handlerRegistry[id]; ok {
			name = tr.GetName()
		}
		return s.staleOnError(id, prev, name, err)
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

func (s *service) destroyFlareSession(ctx context.Context, id int) {
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
	if err := s.px.DestroyFlareSession(ctx, sessionID); err != nil {
		logger.Warn(fmt.Sprintf("%s: flare session destroy: %s", tr.GetName(), err))
	}
}

func (s *service) DestroyFlareSessions(ctx context.Context) {
	// зовется при остановке, чтобы в FlareSolverr не висели брошенные браузеры
	for id := range s.handlerRegistry {
		s.destroyFlareSession(ctx, id)
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
	s.destroyFlareSession(ctx, ttid)

	s.mx.RLock()
	prev = s.clientIndex[ttid]
	s.mx.RUnlock()

	return s.refresh(ctx, ttid, true, prev)
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
	active, paused := trackerActiveDomain(tr), ""
	if active == "" {
		active, paused = s.pickDomain(ctx, tid, tr)
	}
	if paused != "" {
		s.setPause(tid, paused)
		s.reportVerdict(tid, tr.GetName(), paused)
		return pauseError(paused)
	}
	s.reportVerdict(tid, tr.GetName(), "")
	if active != tr.GetDomain() {
		logger.Info(fmt.Sprintf("tracker %s: using mirror %s", tr.GetName(), active))
		tr = withActiveDomain(tr, active)
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
	// первая сборка за процесс без принудительного входа тоже открывает окно loginMaxAge
	if _, seen := s.loginClock().get(tid); forceLogin || !seen {
		s.loginClock().set(tid, time.Now())
	}
	s.mx.Unlock()
	return
}

func (s *service) store(ctx context.Context, tid int, tc client.IClient, forceLogin bool) (err error) {
	var (
		attrs        *tracker.Tracker
		hadCookies   bool
		credsChanged bool
	)

	if attrs, err = s.db.GetTracker(ctx, tid); err != nil {
		return
	}
	hadCookies = attrs.Exists && len(attrs.Cookies) > 0
	stored := attrs.Cookies
	if attrs.Exists {
		// логин берем из настроек, в БД только куки и последние рабочие данные.
		// Пустой логин в настройках рабочий из БД не затирает
		if cfgCreds := tc.GetCredentials(); cfgCreds != "" {
			credsChanged = attrs.Credentials != cfgCreds
			attrs.Credentials = cfgCreds
		}
		attrs.Publicity = tc.GetPublicity()
		forceLogin = forceLogin || credsChanged
	}
	if !forceLogin && attrs.Exists {
		attrs.Cookies = s.liveCookies(tid, tc, attrs.Cookies)
	}
	if err = tc.InitClient(ctx, attrs, forceLogin); err != nil {
		return
	}
	if attrs.Exists {
		cookiesStale := len(tc.Cookies()) > 0 && (forceLogin || !hadCookies || helper.CookiesString(stored) != helper.CookiesString(tc.Cookies()))
		if credsChanged || cookiesStale {
			if len(tc.Cookies()) > 0 {
				attrs.Cookies = tc.Cookies()
			}
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

func (s *service) liveCookies(tid int, tc client.IClient, fallback []*http.Cookie) []*http.Cookie {
	// куки, полученные клиентом за час (Set-Cookie), свежее тех, что лежат в БД
	s.mx.RLock()
	prev := s.clientIndex[tid]
	s.mx.RUnlock()
	if prev == nil || prev.IClient == nil || prev.GetDomain() != tc.GetDomain() {
		return fallback
	}
	if live := prev.Cookies(); len(live) > 0 {
		return live
	}
	return fallback
}

func New(hc *http.Client, downloadHC helper.HTTPDoer, px proxy.IClient, db db, cbCfg *helper.CircuitBreakerConfig, conf tracker.TrackerConfig, alerts *breakeralerts.Registry) *service {
	return &service{
		hc:                 hc,
		downloadHC:         downloadHC,
		px:                 px,
		db:                 db,
		cbCfg:              cbCfg,
		alerts:             alerts,
		handlerRegistry:    buildHandlerRegistry(conf),
		clientIndex:        make(map[int]*CachedTracker),
		logins:             processLogins,
		flareTimeoutCounts: make(map[int]int),
		staleUseCounts:     make(map[int]int),
		domainAlive:        domainProbeWith(hc),
	}
}

func (s *service) newBreaker(name string) *helper.CircuitBreaker {
	cb := helper.NewCircuitBreaker(s.cbCfg)
	cb.TrackerName = name
	cb.Alerts = s.alerts
	return cb
}

func (s *service) Adopt(ctx context.Context, name, hash string) (url string, err error) {
	s.mx.RLock()
	url, ok := s.adopted[hash]
	s.mx.RUnlock()
	if ok {
		return
	}
	for id, tr := range s.handlerRegistry {
		lookup := tr.AdoptLookup(name, hash)
		if lookup == "" {
			continue
		}
		var buf *bytes.Buffer
		if buf, err = s.Response(client.WithNotFoundAnswer(ctx), id, lookup, false); err != nil {
			var se *client.StatusError
			// сеть, 5xx и таймаут не запоминаем, повторим на следующем проходе
			if !errors.As(err, &se) || se.Code != http.StatusNotFound {
				return "", fmt.Errorf("%s: adopt lookup: %w", tr.GetName(), err)
			}
			err = nil
		} else {
			url = tr.AdoptURL(buf)
		}
		// 404 и ответ без релиза значат, что хеш трекеру незнаком. Промах помним
		// пустым адресом, чистке истории он не мешает
		s.mx.Lock()
		if s.adopted == nil {
			s.adopted = make(map[string]string)
		}
		s.adopted[hash] = url
		s.mx.Unlock()
		return
	}
	return
}
