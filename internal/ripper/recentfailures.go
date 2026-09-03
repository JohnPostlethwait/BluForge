package ripper

import (
	"sync"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// recentFailureLimit is how many failed rips keep their captured messages.
//
// A bound rather than a policy: BluForge runs for weeks at a time, and a
// library that fails often would otherwise hold every failure it ever had. Far
// more than anyone scrolls back through, small enough to be irrelevant beside
// one disc scan.
const recentFailureLimit = 50

// recentFailure is what MakeMKV said during one failed rip.
type recentFailure struct {
	messages []makemkv.ScanWarning
	dropped  int
	// seq orders evictions. Job IDs come from the database and rise, but a
	// counter owned here does not depend on that staying true.
	seq uint64
}

// recentFailures keeps the captured messages of the last few failed rips.
//
// It exists because a failed job is removed from the engine's active map before
// it settles, so the activity page finds it again only through the database —
// and the capture is deliberately not written there. Without this the detail
// would reach the log and never the page.
//
// Memory, by decision. A restart drops it, which is the trade-off taken
// knowingly when this was designed: see the "Decisions taken" section of
// docs/superpowers/specs/2026-09-02-logging-levels-and-failure-capture-design.md.
type recentFailures struct {
	mu    sync.RWMutex
	byJob map[int64]recentFailure
	next  uint64
}

func newRecentFailures() *recentFailures {
	return &recentFailures{byJob: make(map[int64]recentFailure)}
}

func (r *recentFailures) record(jobID int64, messages []makemkv.ScanWarning, dropped int) {
	if len(messages) == 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.next++
	r.byJob[jobID] = recentFailure{messages: messages, dropped: dropped, seq: r.next}

	// Evict the oldest until the map is back within its bound. One at a time
	// rather than a sort: this runs once per failed rip, against fifty entries.
	for len(r.byJob) > recentFailureLimit {
		var oldestID int64
		var oldestSeq uint64
		first := true
		for id, f := range r.byJob {
			if first || f.seq < oldestSeq {
				oldestID, oldestSeq, first = id, f.seq, false
			}
		}
		delete(r.byJob, oldestID)
	}
}

func (r *recentFailures) get(jobID int64) ([]makemkv.ScanWarning, int, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.byJob[jobID]
	if !ok {
		return nil, 0, false
	}
	return f.messages, f.dropped, true
}

func (r *recentFailures) count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byJob)
}

// RecentFailure returns what MakeMKV said during a failed rip, if the engine
// still holds it. The second return is how many distinct messages the capture
// turned away; the third reports whether anything was kept at all.
//
// Absent is ordinary, not an error: it is the state of every failure that
// predates the current process.
func (e *Engine) RecentFailure(jobID int64) ([]makemkv.ScanWarning, int, bool) {
	return e.failures.get(jobID)
}

func (e *Engine) recordFailure(jobID int64, messages []makemkv.ScanWarning, dropped int) {
	e.failures.record(jobID, messages, dropped)
}

func (e *Engine) recentFailureCount() int {
	return e.failures.count()
}
