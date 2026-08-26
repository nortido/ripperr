package runner

import (
	"slices"
	"sync"
	"sync/atomic"
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

// forceQueueSize ограничивает, сколько форсированных релизов могут ждать, пока их
// заберёт раннер; лишние отбрасываются, а не блокируют HTTP-обработчик, и
// отброшенный релиз сохраняет флаг force_check до следующего прохода. Размер
// рассчитан на "форсировать всё", ставящее в очередь весь отфильтрованный список разом, а не на клики.
const forceQueueSize = 4096

// normalQueueSize ограничивает, сколько записей могут ждать в обычной очереди трекера.
const normalQueueSize = 4096

func (s *State) Running() bool {
	return s != nil && s.active.Load() > 0
}

func (s *State) Begin(gen uint32) {
	if s != nil && gen == s.gen.Load() {
		s.active.Add(1)
	}
}

func (s *State) End(gen uint32) {
	if s == nil || gen != s.gen.Load() {
		return
	}
	// последняя запись прохода: очередь и текущие записи гасим здесь, иначе
	// подсветка застряла бы на той, которой проход закончился
	if s.active.Add(-1) <= 0 {
		s.clearItems()
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
	s.active.Store(0)
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

// Enqueue помечает запись поставленной в очередь на проверку, Unqueue снимает
// пометку, если поставить в очередь не удалось. Множество живёт только на время
// прохода и нужно интерфейсу, чтобы показать, что ещё будет проверено.
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

// StartItem переводит запись из очереди в работу у воркера трекера ttid.
// Прошлая запись этого трекера остаётся текущей до вызова StartItem со
// следующей: между записями воркер выдерживает паузу лимита, и погасшая на неё
// подсветка мигала бы вхолостую.
func (s *State) StartItem(gen uint32, ttid int, urlHash string) {
	// проверка поколения та же, что у Begin/End: воркер отменённого прохода
	// может успеть взять запись уже после Reset, и без неё он подсветил бы
	// запись, которую никто не проверяет
	if s == nil || urlHash == "" || gen != s.gen.Load() {
		return
	}
	s.itemsMu.Lock()
	defer s.itemsMu.Unlock()
	delete(s.queued, urlHash)
	if s.current == nil {
		s.current = make(map[int]string)
	}
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

// QueuedItems отдаёт не больше limit хешей плюс полный размер очереди: для
// подсветки хватает того, что влезает на страницу, а по общему числу видно, длинный
// проход впереди или одна запись.
func (s *State) QueuedItems(limit int) (hashes []string, total int) {
	if s == nil {
		return nil, 0
	}
	s.itemsMu.Lock()
	defer s.itemsMu.Unlock()
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
