package ripper

import (
	"context"
	"errors"
	"sync"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// RipExecutor is the interface for starting a rip operation.
//
// The source is passed explicitly rather than derived from the drive index,
// because a disc recovered from a spurious AACS directory is ripped from a
// backup folder on disk while still belonging to the drive it came from.
type RipExecutor interface {
	StartRip(ctx context.Context, src makemkv.Source, titleID int, expectSource string, outputDir string, onEvent func(makemkv.Event), selection *makemkv.SelectionOpts) error
}

// Engine manages concurrent rip jobs, enforcing one active rip per drive.
// Additional jobs for the same drive are queued and processed sequentially.
type Engine struct {
	mu       sync.Mutex
	executor RipExecutor
	active   map[int]*Job
	queued   map[int][]*Job // per-drive FIFO queue
	onUpdate func(*Job)
}

// NewEngine creates a new Engine with the given RipExecutor.
func NewEngine(executor RipExecutor) *Engine {
	return &Engine{
		executor: executor,
		active:   make(map[int]*Job),
		queued:   make(map[int][]*Job),
	}
}

// OnUpdate registers a callback invoked whenever a job changes state or progress.
func (e *Engine) OnUpdate(fn func(*Job)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onUpdate = fn
}

// Submit queues a job for execution. If no rip is active on the drive, the job
// starts immediately. Otherwise it is queued and will start automatically when
// the current (and any earlier queued) jobs finish.
func (e *Engine) Submit(job *Job) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.active[job.DriveIndex]; exists {
		e.queued[job.DriveIndex] = append(e.queued[job.DriveIndex], job)
		return nil
	}

	e.active[job.DriveIndex] = job
	go e.run(job)
	return nil
}

// drainQueue starts the next queued job for a drive, if any.
// Must NOT hold e.mu when calling — run() releases the lock before calling this.
func (e *Engine) drainQueue(driveIndex int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	q := e.queued[driveIndex]
	if len(q) == 0 {
		return
	}

	next := q[0]
	e.queued[driveIndex] = q[1:]
	if len(e.queued[driveIndex]) == 0 {
		delete(e.queued, driveIndex)
	}

	e.active[driveIndex] = next
	go e.run(next)
}

// IsActive reports whether a rip is currently running on the given drive.
func (e *Engine) IsActive(driveIndex int) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.active[driveIndex]
	return ok
}

// ActiveJobs returns a snapshot of all currently active jobs.
func (e *Engine) ActiveJobs() []*Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	jobs := make([]*Job, 0, len(e.active))
	for _, j := range e.active {
		jobs = append(jobs, j)
	}
	return jobs
}

// QueuedJobs returns a snapshot of all jobs waiting in per-drive queues.
func (e *Engine) QueuedJobs() []*Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	var jobs []*Job
	for _, q := range e.queued {
		jobs = append(jobs, q...)
	}
	return jobs
}

// ErrRemovedFromQueue is the reason reported for a job cancelled before its
// rip ever started.
var ErrRemovedFromQueue = errors.New("cancelled before the rip started")

// ErrDiscRemoved is the reason reported for a queued job whose disc left the
// drive before its turn came.
var ErrDiscRemoved = errors.New("cancelled: the disc this rip was queued for is no longer in the drive")

// RemoveQueued removes a pending job from the per-drive queue by job ID.
// Returns true if the job was found and removed.
//
// The removed job still gets its OnComplete callback. A queued job that is
// dropped never reaches run, so this is the only place that can settle it, and
// the caller relies on that: the orchestrator's WaitGroup only drops to zero
// once every submitted job has completed. Without this the goroutine waiting on
// it blocks forever, the batch's parent .rip- temp directory is never removed,
// the DB row stays at "ripping", and any claim the job held on a scratch disc
// backup is never released.
func (e *Engine) RemoveQueued(jobID int64) bool {
	e.mu.Lock()
	var removed *Job
search:
	for driveIdx, q := range e.queued {
		for i, j := range q {
			if j.ID == jobID {
				removed = j
				e.queued[driveIdx] = append(q[:i], q[i+1:]...)
				if len(e.queued[driveIdx]) == 0 {
					delete(e.queued, driveIdx)
				}
				break search
			}
		}
	}
	e.mu.Unlock()

	if removed == nil {
		return false
	}

	// Settle the job outside the lock: OnComplete writes to the database,
	// broadcasts, and releases backup claims, any of which can re-enter the
	// engine.
	//
	// A job dropped from the queue was never ripped, so there is nothing for
	// OnComplete to organize and nothing it can report that would change the
	// outcome. It is called for its cleanup — the WaitGroup, the backup claim —
	// and the job is settled here, once, either side of it.
	removed.Fail(ErrRemovedFromQueue.Error())
	if removed.OnComplete != nil {
		_ = removed.OnComplete(removed, ErrRemovedFromQueue)
	}
	e.notify(removed)
	return true
}

// RemoveQueuedForDrive drops every job still waiting on a drive, returning how
// many there were. The job currently running is left alone.
//
// Queued jobs name titles chosen from a particular disc. When that disc leaves,
// each one used to take its turn, start, fail and write a failure row — nine of
// them for a ten-title batch the user interrupted once. And if another disc had
// gone in, they were worse than noise: a list of titles picked from the disc
// that left, about to be read off the one that replaced it, under the old
// disc's names.
//
// The running job is deliberately not cancelled. It will fail on its own if the
// disc really is gone, and an eject is a debounced inference — one that has
// been wrong before — so throwing away a rip that may be most of the way
// through is the worse mistake to make on it.
func (e *Engine) RemoveQueuedForDrive(driveIndex int) int {
	e.mu.Lock()
	dropped := e.queued[driveIndex]
	delete(e.queued, driveIndex)
	e.mu.Unlock()

	// Settle outside the lock: OnComplete writes to the database, broadcasts,
	// and releases backup claims, any of which can re-enter the engine.
	for _, job := range dropped {
		job.Fail(ErrDiscRemoved.Error())
		if job.OnComplete != nil {
			_ = job.OnComplete(job, ErrDiscRemoved)
		}
		e.notify(job)
	}
	return len(dropped)
}

// notify calls the onUpdate callback if one has been registered.
func (e *Engine) notify(job *Job) {
	e.mu.Lock()
	fn := e.onUpdate
	e.mu.Unlock()
	if fn != nil {
		fn(job)
	}
}

// CancelActive cancels the active rip job with the given ID.
// Returns true if the job was found and cancelled.
func (e *Engine) CancelActive(jobID int64) bool {
	e.mu.Lock()
	var target *Job
	for _, j := range e.active {
		if j.ID == jobID {
			target = j
			break
		}
	}
	e.mu.Unlock()

	if target == nil {
		return false
	}
	target.Cancel()
	return true
}

// run executes the rip job, updating status and progress along the way.
func (e *Engine) run(job *Job) {
	ctx, cancel := context.WithCancel(context.Background())
	job.mu.Lock()
	job.cancel = cancel
	job.mu.Unlock()
	defer cancel()

	job.Start()
	e.notify(job)

	if job.OnStart != nil {
		if err := job.OnStart(job); err != nil {
			job.Fail(err.Error())
			// Remove from active map and drain queue so subsequent jobs aren't blocked.
			e.mu.Lock()
			delete(e.active, job.DriveIndex)
			e.mu.Unlock()
			// The rip never began, so there is nothing to organize; OnComplete
			// runs for its cleanup and cannot change an already-settled failure.
			if job.OnComplete != nil {
				_ = job.OnComplete(job, err)
			}
			e.notify(job)
			e.drainQueue(job.DriveIndex)
			return
		}
	}

	// MakeMKV analyses the disc before it copies anything, reporting progress
	// throughout. Until the copy starts, that percentage says nothing about how
	// much of the title exists on disk.
	job.SetPhase(PhaseAnalyzing)

	var lastPct int
	err := ripWithRetry(ctx, e.executor, job, func(ev makemkv.Event) {
		// "Saving N titles into directory" is the copy starting.
		if ev.Type == "MSG" && ev.Message != nil && ev.Message.Code == makemkv.MsgSavingTitles {
			job.SetPhase(PhaseCopying)
			e.notify(job)
		}
		if ev.Type == "PRGV" && ev.Progress != nil {
			p := ev.Progress
			if p.Max > 0 {
				pct := int(float64(p.Total) / float64(p.Max) * 100)
				if pct > 100 {
					pct = 100
				}
				// When progress drops significantly, a new stage has
				// started (e.g. analysis → rip). Reset so we track
				// the new stage from zero.
				if pct < lastPct-5 {
					lastPct = pct
				}
				// Only update when progress advances within the
				// current stage.
				if pct > lastPct {
					lastPct = pct
					job.UpdateProgress(pct)
					e.notify(job)
				}
			}
		}
	})

	// Organizing is the job's state for as long as OnComplete is actually doing
	// the organizing. Announced before the callback runs and left in place
	// until it returns, rather than set and immediately superseded.
	//
	// Only for a rip that produced something: OnComplete runs either way, but
	// on the failure path all it has to do is clean up, and announcing
	// "Organizing" there would name work that is not happening.
	if err == nil {
		job.SetStatus(StatusOrganizing)
		e.notify(job)
	}

	// Remove from active map and start next queued job.
	e.mu.Lock()
	delete(e.active, job.DriveIndex)
	e.mu.Unlock()

	// OnComplete moves the ripped file to its destination. A rip whose file
	// could not be placed has not succeeded, so its error settles the job just
	// as the rip's own would — and nothing terminal is announced until both
	// have had their say.
	postErr := err
	if job.OnComplete != nil {
		if cbErr := job.OnComplete(job, err); cbErr != nil && postErr == nil {
			postErr = cbErr
		}
	}

	if postErr != nil {
		job.Fail(postErr.Error())
	} else {
		job.Complete(job.OutputDir)
	}
	e.notify(job)

	e.drainQueue(job.DriveIndex)
}
