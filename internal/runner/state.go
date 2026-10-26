package runner

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	storageModel "ripperr/internal/model/storage"
	"ripperr/internal/system/logger"
)

type State struct {
	trigger         chan bool
	force           chan string
	active          atomic.Int32
	gen             atomic.Uint32
	normalActive    atomic.Int32
	waitingSettings atomic.Bool
	mu              sync.Mutex
	torrents        torrentLocks
	restart         atomic.Bool
	dry             atomic.Bool
	stopping        atomic.Bool
	itemsMu         sync.Mutex
	queued          map[string]struct{}
	current         map[int]string
	pageMu          sync.Mutex
	pageOK          map[int]bool
	serverErrs      map[string]serverErrMark
	errsRestored    bool
	errsSaveMu      sync.Mutex
	pollMu          sync.Mutex
	pollStateMu     sync.Mutex
	pollFn          func(context.Context) (int, error)
	pollGen         uint32
	pollSeq         uint64
	pollAt          time.Time
	pollCached      bool
	pollResult      int
	pollAbsent      map[string]struct{}
}

func NewState() *State {
	return &State{trigger: make(chan bool, 1), force: make(chan string, forceQueueSize)}
}

// лишние форсы отбрасываем, чтобы не блокировать HTTP-обработчик, флаг force_check
// доживет до следующего прохода. Размер под "форсировать все" на весь фильтр, клики
// столько не наберут
const forceQueueSize = 4096

const normalQueueSize = 4096

func (s *State) Running() bool {
	return s != nil && s.active.Load() > 0
}

func (s *State) Begin(gen uint32) {
	if s == nil {
		return
	}
	// проверка поколения и счетчик под одним замком: иначе Reset между ними оставит 1 навсегда или уведет в минус
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen == s.gen.Load() {
		if s.active.Add(1) == 1 {
			logger.Info("check pass started")
		}
	}
}

func (s *State) End(gen uint32) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen.Load() {
		return
	}
	// последняя запись прохода: очередь и текущие записи гасим здесь, иначе
	// подсветка застряла бы на той, которой проход закончился
	left := s.active.Add(-1)
	if left <= 0 {
		s.clearItems()
	}
	if left == 0 {
		logger.Info("check pass finished")
	}
}

func (s *State) Trigger() <-chan bool {
	if s == nil {
		return nil
	}
	return s.trigger
}

func (s *State) RequestRun() bool {
	if s == nil || s.Running() {
		return false
	}
	select {
	case s.trigger <- false:
		return true
	default:
		return false
	}
}

func (s *State) RequestDryRun() bool {
	if s == nil || s.Running() {
		return false
	}
	select {
	case s.trigger <- true:
		return true
	default:
		return false
	}
}

func (s *State) RequestForce(urlHash string) bool {
	if s == nil || urlHash == "" {
		return false
	}
	select {
	case s.force <- urlHash:
		return true
	default:
		return false
	}
}

func (s *State) Force() <-chan string {
	if s == nil {
		return nil
	}
	return s.force
}

func (s *State) DrainForce() []string {
	if s == nil {
		return nil
	}
	var out []string
	for {
		select {
		case hash := <-s.force:
			out = append(out, hash)
		default:
			return out
		}
	}
}

func (s *State) DryRunning() bool {
	return s != nil && s.dry.Load()
}

func (s *State) setDry(v bool) {
	if s != nil {
		s.dry.Store(v)
	}
}

func (s *State) MarkStopping() {
	if s != nil {
		s.stopping.Store(true)
	}
}

func (s *State) Stopping() bool {
	return s != nil && s.stopping.Load()
}

func (s *State) Reset() uint32 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearItems()
	if s.active.Swap(0) != 0 {
		logger.Info("check pass finished (reset)")
	}
	s.normalActive.Store(0)
	s.dry.Store(false)
	s.stopping.Store(false)
	return s.gen.Add(1)
}

func (s *State) MarkRestart() {
	if s != nil {
		s.restart.Store(true)
	}
}

func (s *State) ConsumeRestart() bool {
	if s == nil {
		return false
	}
	return s.restart.Swap(false)
}

func (s *State) DrainTrigger() {
	if s == nil {
		return
	}
	select {
	case <-s.trigger:
	default:
	}
}

func (s *State) Enqueue(gen uint32, urlHash string) {
	if s == nil || urlHash == "" || gen != s.gen.Load() {
		return
	}
	s.itemsMu.Lock()
	defer s.itemsMu.Unlock()
	if s.queued == nil {
		s.queued = make(map[string]struct{})
	}
	s.queued[urlHash] = struct{}{}
}

func (s *State) Unqueue(gen uint32, urlHash string) {
	if s == nil || urlHash == "" || gen != s.gen.Load() {
		return
	}
	s.itemsMu.Lock()
	defer s.itemsMu.Unlock()
	delete(s.queued, urlHash)
}

func (s *State) StartItem(gen uint32, ttid int, urlHash string) {
	// проверка поколения как у Begin/End: воркер отмененного прохода может взять запись
	// уже после Reset и подсветить то, что никто не проверяет
	if s == nil || urlHash == "" || gen != s.gen.Load() {
		return
	}
	s.itemsMu.Lock()
	defer s.itemsMu.Unlock()
	delete(s.queued, urlHash)
	if s.current == nil {
		s.current = make(map[int]string)
	}
	// прошлая запись трекера остается текущей до следующего StartItem: между записями
	// воркер ждет паузу лимита, и погасшая подсветка мигала бы зря
	s.current[ttid] = urlHash
}

func (s *State) CheckingItems() []string {
	if s == nil {
		return nil
	}
	s.itemsMu.Lock()
	defer s.itemsMu.Unlock()
	out := make([]string, 0, len(s.current))
	for _, hash := range s.current {
		out = append(out, hash)
	}
	slices.Sort(out)
	return out
}

func (s *State) QueuedItems(limit int) (hashes []string, total int) {
	if s == nil {
		return nil, 0
	}
	s.itemsMu.Lock()
	defer s.itemsMu.Unlock()
	// для подсветки хватает страницы, а по total видно, длинный проход впереди или одна запись
	total = len(s.queued)
	if limit <= 0 {
		return nil, total
	}
	hashes = make([]string, 0, min(limit, total))
	for hash := range s.queued {
		if len(hashes) == limit {
			break
		}
		hashes = append(hashes, hash)
	}
	return hashes, total
}

func (s *State) clearItems() {
	if s == nil {
		return
	}
	s.itemsMu.Lock()
	defer s.itemsMu.Unlock()
	s.queued = nil
	s.current = nil
}

func (s *State) BeginNormal(gen uint32) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen == s.gen.Load() {
		s.normalActive.Add(1)
	}
}

func (s *State) EndNormal(gen uint32) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen == s.gen.Load() {
		s.normalActive.Add(-1)
	}
}

func (s *State) NormalRunning() bool {
	return s != nil && s.normalActive.Load() > 0
}

func (s *State) SetWaitingSettings(v bool) {
	if s != nil {
		s.waitingSettings.Store(v)
	}
}

func (s *State) WaitingSettings() bool {
	return s != nil && s.waitingSettings.Load()
}

type torrentGate struct {
	ch   chan struct{}
	refs int
}

type torrentLocks struct {
	mu sync.Mutex
	m  map[string]*torrentGate
}

func (l *torrentLocks) acquire(key string) *torrentGate {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]*torrentGate{}
	}
	g := l.m[key]
	if g == nil {
		g = &torrentGate{ch: make(chan struct{}, 1)}
		l.m[key] = g
	}
	g.refs++
	return g
}

func (l *torrentLocks) release(key string, g *torrentGate) {
	l.mu.Lock()
	defer l.mu.Unlock()
	g.refs--
	if g.refs == 0 {
		delete(l.m, key)
	}
}

func (s *State) LockTorrent(hash string) (unlock func()) {
	if s == nil {
		return func() {}
	}
	key := strings.ToLower(hash)
	g := s.torrents.acquire(key)
	g.ch <- struct{}{}
	return func() {
		<-g.ch
		s.torrents.release(key, g)
	}
}

func (s *State) TryLockTorrent(hash string) (unlock func(), ok bool) {
	if s == nil {
		return func() {}, true
	}
	key := strings.ToLower(hash)
	g := s.torrents.acquire(key)
	select {
	case g.ch <- struct{}{}:
		return func() {
			<-g.ch
			s.torrents.release(key, g)
		}, true
	default:
		s.torrents.release(key, g)
		return nil, false
	}
}

const serverErrsSetting = "runner_server_errors"

// два backoffCap: релиз с отметкой перепроверяется в пределах суток, так что стареют только пропавшие релизы или долгий простой
const serverErrTTL = 2 * backoffCap

type serverErrMark struct {
	count int
	at    time.Time
}

type serverErrJSON struct {
	Count int       `json:"count"`
	At    time.Time `json:"at"`
}

func (s *State) PageServerError(ttid int, urlHash string, now time.Time) (counted bool) {
	if s == nil || urlHash == "" {
		return false
	}
	s.pageMu.Lock()
	defer s.pageMu.Unlock()
	// считаем 5xx только после успешной страницы этого трекера: при сбое всего трекера пауза не растет ни у кого
	if s.pageOK[ttid] {
		if s.serverErrs == nil {
			s.serverErrs = make(map[string]serverErrMark)
		}
		m := s.serverErrs[urlHash]
		s.serverErrs[urlHash] = serverErrMark{count: m.count + 1, at: now}
		counted = true
	}
	if s.pageOK == nil {
		s.pageOK = make(map[int]bool)
	}
	s.pageOK[ttid] = false
	return counted
}

func (s *State) PageOK(ttid int, urlHash string) (cleared bool) {
	if s == nil {
		return false
	}
	s.pageMu.Lock()
	defer s.pageMu.Unlock()
	if s.pageOK == nil {
		s.pageOK = make(map[int]bool)
	}
	s.pageOK[ttid] = true
	_, cleared = s.serverErrs[urlHash]
	delete(s.serverErrs, urlHash)
	return cleared
}

func (s *State) RestoreServerErrors(raw json.RawMessage, now time.Time) error {
	if s == nil {
		return nil
	}
	s.pageMu.Lock()
	defer s.pageMu.Unlock()
	// флаг ставим и при битом JSON: повторная загрузка не должна затирать свежие отметки
	if s.errsRestored {
		return nil
	}
	s.errsRestored = true
	if len(raw) == 0 {
		return nil
	}
	var stored map[string]serverErrJSON
	if err := json.Unmarshal(raw, &stored); err != nil {
		return err
	}
	for hash, m := range stored {
		if hash == "" || m.Count < 1 || m.At.IsZero() || now.Sub(m.At) > serverErrTTL {
			continue
		}
		if _, ok := s.serverErrs[hash]; ok {
			continue
		}
		if s.serverErrs == nil {
			s.serverErrs = make(map[string]serverErrMark)
		}
		s.serverErrs[hash] = serverErrMark{count: m.Count, at: m.At}
	}
	return nil
}

func (s *State) SaveServerErrors(ctx context.Context, now time.Time, save func(context.Context, string, json.RawMessage) error) error {
	if s == nil {
		return nil
	}
	// замок держим и на сохранении: иначе старый снимок мог бы лечь в БД после нового
	s.errsSaveMu.Lock()
	defer s.errsSaveMu.Unlock()
	s.pageMu.Lock()
	// пока сохраненные отметки не загружены, снимок только из памяти затер бы их в БД
	if !s.errsRestored {
		s.pageMu.Unlock()
		return nil
	}
	out := make(map[string]serverErrJSON, len(s.serverErrs))
	for hash, m := range s.serverErrs {
		if now.Sub(m.at) > serverErrTTL {
			delete(s.serverErrs, hash)
			continue
		}
		out[hash] = serverErrJSON{Count: m.count, At: m.at}
	}
	s.pageMu.Unlock()
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return save(ctx, serverErrsSetting, raw)
}

func (s *State) ServerErrorMark(urlHash string) (storageModel.ErrorMark, bool) {
	if s == nil {
		return storageModel.ErrorMark{}, false
	}
	s.pageMu.Lock()
	defer s.pageMu.Unlock()
	m, ok := s.serverErrs[urlHash]
	return storageModel.ErrorMark{Streak: m.count, CheckedAt: m.at}, ok
}

var ErrPollUnavailable = errors.New("runner is not ready")

var pollMinGap = 5 * time.Second

func (s *State) SetPoller(gen uint32, fn func(context.Context) (int, error)) {
	if s == nil {
		return
	}
	// замок поколения как у Begin: Reset между проверкой и записью оставил бы опрос мертвого поколения
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pollStateMu.Lock()
	defer s.pollStateMu.Unlock()
	if fn != nil {
		if gen != s.gen.Load() {
			return
		}
	} else if s.pollFn == nil || s.pollGen != gen {
		return
	}
	s.pollFn = fn
	s.pollGen = gen
	s.pollSeq++
	s.pollCached = false
	// метки прежнего опроса не переживают смену опроса
	s.pollAbsent = nil
}

func (s *State) SetAbsent(gen uint32, hashes map[string]struct{}) int {
	if s == nil {
		return 0
	}
	s.pollStateMu.Lock()
	defer s.pollStateMu.Unlock()
	if s.pollFn == nil || s.pollGen != gen {
		return 0
	}
	flipped := 0
	for h := range hashes {
		if _, ok := s.pollAbsent[h]; !ok {
			flipped++
		}
	}
	for h := range s.pollAbsent {
		if _, ok := hashes[h]; !ok {
			flipped++
		}
	}
	next := make(map[string]struct{}, len(hashes))
	for h := range hashes {
		next[h] = struct{}{}
	}
	s.pollAbsent = next
	return flipped
}

func (s *State) Absent() map[string]struct{} {
	if s == nil {
		return nil
	}
	s.pollStateMu.Lock()
	defer s.pollStateMu.Unlock()
	return s.pollAbsent
}

func (s *State) PollNew(ctx context.Context) (int, error) {
	if s == nil {
		return 0, ErrPollUnavailable
	}
	s.pollMu.Lock()
	defer s.pollMu.Unlock()
	s.pollStateMu.Lock()
	fn, seq := s.pollFn, s.pollSeq
	if fn == nil {
		s.pollStateMu.Unlock()
		return 0, ErrPollUnavailable
	}
	if s.pollCached && time.Since(s.pollAt) < pollMinGap {
		n := s.pollResult
		s.pollStateMu.Unlock()
		return n, nil
	}
	s.pollStateMu.Unlock()

	n, err := fn(ctx)
	if err != nil {
		return n, err
	}
	s.pollStateMu.Lock()
	defer s.pollStateMu.Unlock()
	// SetPoller за время опроса сменил или снял опрос: результат старого в кэш не кладем
	if s.pollSeq == seq {
		s.pollCached, s.pollResult, s.pollAt = true, n, time.Now()
	}
	return n, nil
}
