package ripper

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// blockingRipExecutor blocks until its channel is closed or its context is cancelled.
// It writes a dummy file to outputDir so the engine's cleanup path doesn't fail.
type blockingRipExecutor struct {
	started int32
	block   chan struct{}
}

func newBlockingRipExecutor() *blockingRipExecutor {
	return &blockingRipExecutor{
		block: make(chan struct{}),
	}
}

func (b *blockingRipExecutor) release() {
	close(b.block)
}

func (b *blockingRipExecutor) StartRip(ctx context.Context, _ int, _ int, outputDir string, _ func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	atomic.AddInt32(&b.started, 1)

	// Write a dummy file so any post-rip cleanup that expects output won't fail.
	if outputDir != "" {
		_ = os.MkdirAll(outputDir, 0o755)
		_ = os.WriteFile(filepath.Join(outputDir, "title.mkv"), []byte("dummy"), 0o644)
	}

	select {
	case <-b.block:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *blockingRipExecutor) startedCount() int {
	return int(atomic.LoadInt32(&b.started))
}

// waitForStarted polls until the executor's started count reaches n.
func (b *blockingRipExecutor) waitForStarted(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if b.startedCount() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d StartRip calls; got %d", n, b.startedCount())
}

func TestEngine_IsActive(t *testing.T) {
	exec := newBlockingRipExecutor()
	engine := NewEngine(exec)

	job := NewJob(0, 1, "DISC", t.TempDir())
	done := make(chan struct{})
	job.OnComplete = func(_ *Job, _ error) { close(done) }

	if err := engine.Submit(job); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	exec.waitForStarted(t, 1)

	if !engine.IsActive(0) {
		t.Error("IsActive(0) = false, want true while job is running")
	}
	if engine.IsActive(1) {
		t.Error("IsActive(1) = true, want false (no job on drive 1)")
	}

	exec.release()
	<-done

	// After completion, give engine time to clean up active map.
	time.Sleep(20 * time.Millisecond)
	if engine.IsActive(0) {
		t.Error("IsActive(0) = true after job completed, want false")
	}
}

func TestEngine_ActiveJobs(t *testing.T) {
	exec := newBlockingRipExecutor()
	engine := NewEngine(exec)

	job := NewJob(0, 1, "DISC", t.TempDir())
	done := make(chan struct{})
	job.OnComplete = func(_ *Job, _ error) { close(done) }

	if err := engine.Submit(job); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	exec.waitForStarted(t, 1)

	active := engine.ActiveJobs()
	if len(active) != 1 {
		t.Fatalf("ActiveJobs() len = %d, want 1", len(active))
	}

	exec.release()
	<-done

	time.Sleep(20 * time.Millisecond)
	active = engine.ActiveJobs()
	if len(active) != 0 {
		t.Fatalf("ActiveJobs() len = %d after completion, want 0", len(active))
	}
}

func TestEngine_QueuedJobs(t *testing.T) {
	exec := newBlockingRipExecutor()
	engine := NewEngine(exec)

	job1 := NewJob(0, 1, "DISC", t.TempDir())
	done1 := make(chan struct{})
	job1.OnComplete = func(_ *Job, _ error) { close(done1) }

	if err := engine.Submit(job1); err != nil {
		t.Fatalf("submit job1 failed: %v", err)
	}
	exec.waitForStarted(t, 1)

	job2 := NewJob(0, 2, "DISC", t.TempDir())
	done2 := make(chan struct{})
	job2.OnComplete = func(_ *Job, _ error) { close(done2) }

	if err := engine.Submit(job2); err != nil {
		t.Fatalf("submit job2 failed: %v", err)
	}

	queued := engine.QueuedJobs()
	if len(queued) != 1 {
		t.Fatalf("QueuedJobs() len = %d, want 1", len(queued))
	}

	exec.release()
	<-done1
	<-done2

	time.Sleep(20 * time.Millisecond)
	queued = engine.QueuedJobs()
	if len(queued) != 0 {
		t.Fatalf("QueuedJobs() len = %d after all complete, want 0", len(queued))
	}
}

func TestEngine_RemoveQueued(t *testing.T) {
	exec := newBlockingRipExecutor()
	engine := NewEngine(exec)

	job1 := NewJob(0, 1, "DISC", t.TempDir())
	done := make(chan struct{})
	job1.OnComplete = func(_ *Job, _ error) { close(done) }

	if err := engine.Submit(job1); err != nil {
		t.Fatalf("submit job1 failed: %v", err)
	}
	exec.waitForStarted(t, 1)

	job2 := NewJob(0, 2, "DISC", t.TempDir())
	job2.ID = 42
	if err := engine.Submit(job2); err != nil {
		t.Fatalf("submit job2 failed: %v", err)
	}

	if !engine.RemoveQueued(42) {
		t.Error("RemoveQueued(42) = false, want true")
	}
	if engine.RemoveQueued(42) {
		t.Error("RemoveQueued(42) second call = true, want false (already removed)")
	}

	queued := engine.QueuedJobs()
	if len(queued) != 0 {
		t.Fatalf("QueuedJobs() len = %d after removal, want 0", len(queued))
	}

	exec.release()
	<-done
}

func TestEngine_RemoveQueued_NotFound(t *testing.T) {
	exec := newBlockingRipExecutor()
	engine := NewEngine(exec)

	if engine.RemoveQueued(999) {
		t.Error("RemoveQueued(999) = true with no jobs, want false")
	}
}

func TestEngine_CancelActive(t *testing.T) {
	exec := newBlockingRipExecutor()
	engine := NewEngine(exec)

	job := NewJob(0, 1, "DISC", t.TempDir())
	job.ID = 1
	done := make(chan struct{})
	job.OnComplete = func(_ *Job, _ error) { close(done) }

	if err := engine.Submit(job); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	exec.waitForStarted(t, 1)

	if !engine.CancelActive(1) {
		t.Error("CancelActive(1) = false, want true")
	}

	// Wait for the job to finish (context cancellation should unblock the executor).
	select {
	case <-done:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("job did not complete after CancelActive")
	}

	if engine.CancelActive(999) {
		t.Error("CancelActive(999) = true, want false (no such job)")
	}
}
