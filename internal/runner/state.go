package runner

import (
	"slices"
	"sync"
	"sync/atomic"

	"ripperr/internal/system/logger"
)

type State struct {
	trigger  chan bool
	force    chan string
	active   atomic.Int32
	gen      atomic.Uint32
	restart  atomic.Bool
	dry      atomic.Bool
	stopping atomic.Bool
	itemsMu  sync.Mutex
	queued   map[string]struct{}
	current  map[int]string
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
	if s != nil && gen == s.gen.Load() {
		if s.active.Add(1) == 1 {
			logger.Info("check pass started")
		}
	}
}

func (s *State) End(gen uint32) {
	if s == nil || gen != s.gen.Load() {
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
	s.clearItems()
	if s.active.Swap(0) != 0 {
		logger.Info("check pass finished (reset)")
	}
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
