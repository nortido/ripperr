package runner

import (
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
	if s != nil && gen == s.gen.Load() {
		s.active.Add(-1)
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
