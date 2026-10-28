package tracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"time"

	"ripperr/internal/helper"
	proxyModel "ripperr/internal/model/proxy"
	"ripperr/internal/system/logger"
	"ripperr/internal/tracker/client"
)

const (
	mirrorFailThreshold = 2
	mirrorFailWindow    = 5 * time.Minute
	mirrorCooldown      = 10 * time.Minute
)

type mirrorRotation struct {
	mu        sync.Mutex
	now       func() time.Time
	fails     map[int][]time.Time
	tried     map[int]time.Time
	sticky    map[int]string
	hook      func(tracker, from, to string)
	paused    map[int]mirrorPause
	verdicts  map[int]string
	pauseHook func(ttid int, host string)
}

type mirrorPause struct {
	host  string
	until time.Time
}

func (r *mirrorRotation) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

func mirrorFailure(ctx context.Context, err error, flare bool) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, helper.ErrCircuitOpen) {
		return false
	}
	var se *client.StatusError
	if errors.As(err, &se) {
		return se.Code >= 500
	}
	// у flare-трекера сетевая ошибка идет от FlareSolverr, трекер тут ни при чем
	if flare {
		return false
	}
	var ue *url.Error
	// *url.Error сам реализует net.Error, поэтому разбор URL (Op "parse") отсекаем до проверки net.Error
	if errors.As(err, &ue) {
		return ue.Op != "parse"
	}
	var ne net.Error
	return errors.As(err, &ne)
}

func (s *service) SetMirrorSwitchHook(hook func(tracker, from, to string)) {
	s.rot.mu.Lock()
	s.rot.hook = hook
	s.rot.mu.Unlock()
}

func (s *service) SetMirrorPauseHook(hook func(ttid int, host string)) {
	s.rot.mu.Lock()
	s.rot.pauseHook = hook
	s.rot.mu.Unlock()
}

func pauseError(host string) error {
	return fmt.Errorf("%w: %w: %s", client.ErrTrackerUnavailable, ErrMirrorUnconfirmed, host)
}

func (s *service) reportVerdict(ttid int, name, host string) {
	s.rot.mu.Lock()
	prev, seen := s.rot.verdicts[ttid]
	if seen && prev == host {
		s.rot.mu.Unlock()
		return
	}
	if s.rot.verdicts == nil {
		s.rot.verdicts = make(map[int]string)
	}
	s.rot.verdicts[ttid] = host
	hook := s.rot.pauseHook
	s.rot.mu.Unlock()
	if host != "" {
		logger.Warn(fmt.Sprintf("tracker %s: no confirmed host answers, %s waits for confirmation", name, host))
	}
	if hook != nil {
		hook(ttid, host)
	}
}

func (s *service) setPause(ttid int, host string) {
	until := s.rot.clock().Add(mirrorCooldown)
	s.rot.mu.Lock()
	if s.rot.paused == nil {
		s.rot.paused = make(map[int]mirrorPause)
	}
	s.rot.paused[ttid] = mirrorPause{host: host, until: until}
	s.rot.mu.Unlock()
}

func (s *service) pausedHost(ttid int) (host string, resume bool) {
	now := s.rot.clock()
	s.rot.mu.Lock()
	defer s.rot.mu.Unlock()
	p, ok := s.rot.paused[ttid]
	if !ok {
		return "", false
	}
	if now.Before(p.until) {
		return p.host, false
	}
	delete(s.rot.paused, ttid)
	return p.host, true
}

func (s *service) failThreshold() int {
	n := 1
	if s.cbCfg != nil && s.cbCfg.MaxFailures > n {
		n = s.cbCfg.MaxFailures
	}
	return min(n, mirrorFailThreshold)
}

func (s *service) failWindow(ttid int) time.Duration {
	w := mirrorFailWindow
	if limit := s.GetHourLimit(ttid); limit > 0 {
		w = max(w, 2*time.Hour/time.Duration(limit))
	}
	return w
}

func (s *service) noteFailure(ctx context.Context, ttid int, err error) bool {
	tr, ok := s.handlerRegistry[ttid]
	if !ok || !mirrorFailure(ctx, err, tr.GetProxyType() == proxyModel.TypeFlare) {
		return false
	}
	now := s.rot.clock()
	window := s.failWindow(ttid)
	s.rot.mu.Lock()
	defer s.rot.mu.Unlock()
	if s.rot.fails == nil {
		s.rot.fails = make(map[int][]time.Time)
	}
	kept := s.rot.fails[ttid][:0]
	for _, at := range s.rot.fails[ttid] {
		if now.Sub(at) <= window {
			kept = append(kept, at)
		}
	}
	kept = append(kept, now)
	s.rot.fails[ttid] = kept
	return len(kept) >= s.failThreshold()
}

func (s *service) noteSuccess(ttid int) {
	s.rot.mu.Lock()
	delete(s.rot.fails, ttid)
	s.rot.mu.Unlock()
}

func (s *service) afterPage(ctx context.Context, ttid int, ct *CachedTracker, link string, buf *bytes.Buffer, err error) (*bytes.Buffer, error) {
	if err == nil {
		s.noteSuccess(ttid)
		return buf, nil
	}
	if !s.noteFailure(ctx, ttid, err) {
		return buf, err
	}
	next, ok, pauseErr := s.rotate(ctx, ttid, ct)
	if pauseErr != nil {
		return buf, pauseErr
	}
	if !ok {
		return buf, err
	}
	buf, err = s.readResponseBody(ctx, ttid, next, link, false)
	if err == nil {
		s.noteSuccess(ttid)
	} else {
		s.noteFailure(ctx, ttid, err)
	}
	return buf, err
}

func (s *service) rotate(ctx context.Context, ttid int, ct *CachedTracker) (*CachedTracker, bool, error) {
	tr, ok := s.handlerRegistry[ttid]
	if !ok || ct == nil || ct.IClient == nil || trackerActiveDomain(tr) != "" || len(trackerMirrors(tr)) == 0 {
		return nil, false, nil
	}
	now := s.rot.clock()
	s.rot.mu.Lock()
	if last, ok := s.rot.tried[ttid]; ok && now.Sub(last) < mirrorCooldown {
		s.rot.mu.Unlock()
		return nil, false, nil
	}
	if s.rot.tried == nil {
		s.rot.tried = make(map[int]time.Time)
	}
	s.rot.tried[ttid] = now
	count := len(s.rot.fails[ttid])
	s.rot.mu.Unlock()

	name, from := tr.GetName(), ct.GetDomain()
	alive := s.domainAlive(tr)
	confirmed, unconfirmed := confirmedMirrors(tr)
	candidate := firstAnswering(ctx, append([]string{tr.GetDomain()}, confirmed...), alive, from)
	if candidate == "" {
		// текущий хост еще отвечает, значит сбой временный: паузы нет
		if host := firstAnswering(ctx, unconfirmed, alive, from); host != "" && !alive(ctx, from) {
			s.setPause(ttid, host)
			// клиент на мертвом подтвержденном хосте больше не отдаем, после паузы логин заново
			s.mx.Lock()
			delete(s.clientIndex, ttid)
			s.mx.Unlock()
			s.reportVerdict(ttid, name, host)
			return nil, false, pauseError(host)
		}
		// без подтвержденных зеркал ведем себя как трекер без зеркал
		if len(confirmed) == 0 && len(unconfirmed) > 0 {
			return nil, false, nil
		}
		logger.Warn(fmt.Sprintf("tracker %s: %d network failures on %s, no alive mirror to switch to", name, count, from))
		return nil, false, nil
	}

	prev := s.setSticky(ttid, candidate)
	next, err := s.relogin(ctx, ttid)
	if err != nil || next == nil || next.IClient == nil || next.GetDomain() != candidate {
		s.setSticky(ttid, prev)
		logger.Warn(fmt.Sprintf("tracker %s: %d network failures on %s, switch to %s failed: %v", name, count, from, candidate, err))
		return nil, false, nil
	}
	s.noteSuccess(ttid)
	logger.Warn(fmt.Sprintf("tracker %s: %d network failures on %s, switched to %s", name, count, from, candidate))
	s.rot.mu.Lock()
	hook := s.rot.hook
	s.rot.mu.Unlock()
	if hook != nil {
		hook(name, from, candidate)
	}
	return next, true, nil
}

func (s *service) setSticky(ttid int, domain string) (prev string) {
	s.rot.mu.Lock()
	defer s.rot.mu.Unlock()
	prev = s.rot.sticky[ttid]
	if domain == "" {
		delete(s.rot.sticky, ttid)
		return
	}
	if s.rot.sticky == nil {
		s.rot.sticky = make(map[int]string)
	}
	s.rot.sticky[ttid] = domain
	return
}

func (s *service) pickDomain(ctx context.Context, ttid int, tr client.ITracker) (domain, paused string) {
	alive := s.domainAlive(tr)
	s.rot.mu.Lock()
	d := s.rot.sticky[ttid]
	s.rot.mu.Unlock()
	if d != "" {
		if alive(ctx, d) {
			return d, ""
		}
		s.setSticky(ttid, "")
	}
	return guardedDomain(ctx, tr, alive)
}
