package runner

import (
	"fmt"
	"sync/atomic"
	"time"

	"trackerr/internal/system/logger"
)

// dryRunLimit caps how many entries a single dry run inspects.
const dryRunLimit = 30

// dryReport aggregates the outcome of a dry run. The last processed entry logs
// the summary: push holds a distribution token until everything is queued, so
// the counter cannot reach zero while entries are still being distributed.
type dryReport struct {
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

// add registers one queued entry. Must be called before the entry is sent to a
// tracker channel, otherwise a fast worker could zero the counter too early.
func (r *dryReport) add() {
	if r == nil {
		return
	}
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

// done marks one entry as processed and logs the summary when nothing is left.
func (r *dryReport) done() {
	if r == nil {
		return
	}
	if r.remaining.Add(-1) == 0 {
		r.finish()
	}
}

// release drops the distribution token once push has queued everything.
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
