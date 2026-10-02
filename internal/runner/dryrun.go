package runner

import (
	"fmt"
	"sync/atomic"
	"time"

	"ripperr/internal/system/logger"
)

const dryRunLimit = 30

type dryReport struct {
	// сводку пишет тот, кто обнулит счетчик. Единицу держит push, пока раздает записи,
	// поэтому ноль раньше конца не наступит
	remaining atomic.Int32
	queued    atomic.Int32
	updated   atomic.Int32
	unchanged atomic.Int32
	unknown   atomic.Int32
	errors    atomic.Int32
	start     time.Time
	state     *State
}

func newDryReport(state *State) *dryReport {
	r := &dryReport{start: time.Now(), state: state}
	r.remaining.Store(1)
	return r
}

func (r *dryReport) add() {
	if r == nil {
		return
	}
	// звать до отправки в канал трекера, иначе быстрый воркер обнулит счетчик раньше времени
	r.queued.Add(1)
	r.remaining.Add(1)
}

func (r *dryReport) count(status string) {
	if r == nil {
		return
	}
	switch status {
	case "updated":
		r.updated.Add(1)
	case "unchanged":
		r.unchanged.Add(1)
	case "unknown":
		r.unknown.Add(1)
	default:
		r.errors.Add(1)
	}
}

func (r *dryReport) done() {
	if r == nil {
		return
	}
	if r.remaining.Add(-1) == 0 {
		r.finish()
	}
}

func (r *dryReport) release() {
	r.done()
}

func (r *dryReport) finish() {
	logger.Info(fmt.Sprintf(
		"dry-run finished: checked %d, would update %d, unchanged %d, unknown %d, errors %d, elapsed %s",
		r.queued.Load(), r.updated.Load(), r.unchanged.Load(), r.unknown.Load(), r.errors.Load(),
		time.Since(r.start).Truncate(time.Second),
	))
	r.state.setDry(false)
}
