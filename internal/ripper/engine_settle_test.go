package ripper

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// instantRipExecutor returns immediately with the configured error.
type instantRipExecutor struct{ err error }

func (m *instantRipExecutor) StartRip(_ context.Context, _ makemkv.Source, _ int, _ string, _ string, _ func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	return m.err
}

// notifyRecorder collects every status the engine announces, in order.
type notifyRecorder struct {
	mu       sync.Mutex
	statuses []JobStatus
	errs     []string
}

func (r *notifyRecorder) record(j *Job) {
	snap := j.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses = append(r.statuses, snap.Status)
	r.errs = append(r.errs, snap.Error)
}

func (r *notifyRecorder) seen() ([]JobStatus, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]JobStatus(nil), r.statuses...), append([]string(nil), r.errs...)
}

func waitFor(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the job to settle")
	}
}

// The rip is only half the job: the file still has to be moved to its
// destination, and that is what OnComplete does. The engine used to call
// Complete() and announce it *before* running OnComplete, so a move that failed
// on a full disk left the page saying "completed" while the database said
// "failed". The terminal announcement has to come from what actually happened.
func TestEngine_JobFailsWhenPostProcessingFails(t *testing.T) {
	rec := &notifyRecorder{}
	engine := NewEngine(&instantRipExecutor{})
	engine.OnUpdate(rec.record)

	done := make(chan struct{})
	job := NewJob(0, 1, "DISC", t.TempDir())
	job.OnComplete = func(_ *Job, _ error) error {
		defer close(done)
		return errors.New("organize: no space left on device")
	}

	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitFor(t, done)

	statuses, errs := rec.seen()
	if len(statuses) == 0 {
		t.Fatal("engine announced nothing")
	}
	final := statuses[len(statuses)-1]
	if final != StatusFailed {
		t.Errorf("final status = %q, want %q — post-processing failed", final, StatusFailed)
	}
	if got := errs[len(errs)-1]; got != "organize: no space left on device" {
		t.Errorf("final error = %q, want the error OnComplete returned", got)
	}

	for i, s := range statuses {
		if s == StatusCompleted {
			t.Errorf("announcement %d claimed %q for a job whose post-processing failed", i, s)
		}
	}
}

// A job that rips and organizes cleanly still has to end at completed.
func TestEngine_JobCompletesWhenPostProcessingSucceeds(t *testing.T) {
	rec := &notifyRecorder{}
	engine := NewEngine(&instantRipExecutor{})
	engine.OnUpdate(rec.record)

	done := make(chan struct{})
	job := NewJob(0, 1, "DISC", t.TempDir())
	job.OnComplete = func(_ *Job, _ error) error {
		defer close(done)
		return nil
	}

	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitFor(t, done)

	statuses, _ := rec.seen()
	if final := statuses[len(statuses)-1]; final != StatusCompleted {
		t.Errorf("final status = %q, want %q", final, StatusCompleted)
	}
}

// "Organizing" was set and then immediately superseded by Complete() before the
// organizing had happened, so it named a phase that was never running. It has
// to be the job's state for as long as the move actually takes.
func TestEngine_JobIsOrganizingWhileOnCompleteRuns(t *testing.T) {
	engine := NewEngine(&instantRipExecutor{})

	done := make(chan struct{})
	var duringOnComplete JobStatus
	job := NewJob(0, 1, "DISC", t.TempDir())
	job.OnComplete = func(j *Job, _ error) error {
		duringOnComplete = j.Snapshot().Status
		close(done)
		return nil
	}

	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitFor(t, done)

	if duringOnComplete != StatusOrganizing {
		t.Errorf("status during OnComplete = %q, want %q", duringOnComplete, StatusOrganizing)
	}
}

// There is nothing to organize for a rip that produced no file. OnComplete
// still runs — it has cleanup to do — but announcing "Organizing" for a rip
// that just failed describes work that is not happening.
func TestEngine_FailedRipIsNeverAnnouncedAsOrganizing(t *testing.T) {
	rec := &notifyRecorder{}
	engine := NewEngine(&instantRipExecutor{err: errors.New("disc read failed")})
	engine.OnUpdate(rec.record)

	done := make(chan struct{})
	job := NewJob(0, 1, "DISC", t.TempDir())
	job.OnComplete = func(_ *Job, _ error) error {
		defer close(done)
		return nil
	}

	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitFor(t, done)

	statuses, _ := rec.seen()
	for i, s := range statuses {
		if s == StatusOrganizing {
			t.Errorf("announcement %d said %q for a rip that failed", i, s)
		}
	}
}

// A rip that failed never reaches the organize step, and OnComplete's return
// must not be able to paper over it.
func TestEngine_RipFailureSurvivesASilentOnComplete(t *testing.T) {
	rec := &notifyRecorder{}
	engine := NewEngine(&instantRipExecutor{err: errors.New("disc read failed")})
	engine.OnUpdate(rec.record)

	done := make(chan struct{})
	job := NewJob(0, 1, "DISC", t.TempDir())
	job.OnComplete = func(_ *Job, _ error) error {
		defer close(done)
		return nil
	}

	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitFor(t, done)

	statuses, errs := rec.seen()
	if final := statuses[len(statuses)-1]; final != StatusFailed {
		t.Errorf("final status = %q, want %q", final, StatusFailed)
	}
	if got := errs[len(errs)-1]; got != "disc read failed" {
		t.Errorf("final error = %q, want the rip's own error", got)
	}
}
