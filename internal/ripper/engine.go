package ripper

import (
	"context"
	"sync"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// RipExecutor is the interface for starting a rip operation.
type RipExecutor interface {
	StartRip(ctx context.Context, driveIndex int, titleID int, outputDir string, onEvent func(makemkv.Event)) error
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

// notify calls the onUpdate callback if one has been registered.
func (e *Engine) notify(job *Job) {
	e.mu.Lock()
	fn := e.onUpdate
	e.mu.Unlock()
	if fn != nil {
		fn(job)
	}
}

// run executes the rip job, updating status and progress along the way.
func (e *Engine) run(job *Job) {
	job.Start()
	e.notify(job)

	err := e.executor.StartRip(context.Background(), job.DriveIndex, job.TitleIndex, job.OutputDir, func(ev makemkv.Event) {
		if ev.Type == "PRGV" && ev.Progress != nil {
			p := ev.Progress
			if p.Max > 0 {
				pct := int(float64(p.Current) / float64(p.Max) * 100)
				if pct > 100 {
					pct = 100
				}
				job.UpdateProgress(pct)
				e.notify(job)
			}
		}
	})

	if err != nil {
		job.Fail(err.Error())
		e.notify(job)
	} else {
		// Transition through Organizing before completing.
		func() {
			job.mu.Lock()
			defer job.mu.Unlock()
			job.Status = StatusOrganizing
		}()
		e.notify(job)

		job.Complete(job.OutputDir)
		e.notify(job)
	}

	// Remove from active map and start next queued job.
	e.mu.Lock()
	delete(e.active, job.DriveIndex)
	e.mu.Unlock()

	if job.OnComplete != nil {
		job.OnComplete(job, err)
	}

	e.drainQueue(job.DriveIndex)
}
