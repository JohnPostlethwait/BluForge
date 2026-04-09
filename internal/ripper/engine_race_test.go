package ripper

import (
	"context"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// noopRipExecutor completes instantly without error (for race tests).
type noopRipExecutor struct{}

func (noopRipExecutor) StartRip(_ context.Context, _ int, _ int, _ string, _ func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	return nil
}

func TestEngine_ConcurrentSubmitAndQuery(t *testing.T) {
	engine := NewEngine(noopRipExecutor{})

	var wg sync.WaitGroup

	// 5 submitter goroutines, each submitting 10 jobs on a unique drive index.
	const submitters = 5
	const jobsPerSubmitter = 10

	for g := 0; g < submitters; g++ {
		wg.Add(1)
		go func(driveIdx int) {
			defer wg.Done()
			for i := 0; i < jobsPerSubmitter; i++ {
				job := NewJob(driveIdx, i, "DISC", t.TempDir())
				_ = engine.Submit(job)
			}
		}(g)
	}

	// 5 reader goroutines that query engine state repeatedly.
	const readers = 5
	const readIterations = 100

	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < readIterations; i++ {
				_ = engine.IsActive(id % submitters)
				_ = engine.ActiveJobs()
				_ = engine.QueuedJobs()
			}
		}(g)
	}

	wg.Wait()
}
