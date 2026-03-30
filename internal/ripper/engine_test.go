package ripper

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// mockRipExecutor is a test double for RipExecutor.
type mockRipExecutor struct {
	mu    sync.Mutex
	calls int32
	// block controls whether StartRip blocks until released.
	block chan struct{}
}

func newMockRipExecutor() *mockRipExecutor {
	return &mockRipExecutor{
		block: make(chan struct{}),
	}
}

// release unblocks any waiting StartRip calls.
func (m *mockRipExecutor) release() {
	close(m.block)
}

func (m *mockRipExecutor) StartRip(_ context.Context, _ int, _ int, _ string, _ func(makemkv.Event)) error {
	atomic.AddInt32(&m.calls, 1)
	// Block until released so we can observe concurrent state.
	<-m.block
	return nil
}

func (m *mockRipExecutor) callCount() int {
	return int(atomic.LoadInt32(&m.calls))
}

func TestEngineQueuesSecondRipOnSameDrive(t *testing.T) {
	mock := newMockRipExecutor()
	engine := NewEngine(mock)

	job1 := NewJob(0, 1, "DISC", "/output")
	if err := engine.Submit(job1); err != nil {
		t.Fatalf("first submit on drive 0 should succeed, got: %v", err)
	}

	// Give the goroutine time to start and enter StartRip.
	time.Sleep(20 * time.Millisecond)

	job2 := NewJob(0, 2, "DISC", "/output")
	if err := engine.Submit(job2); err != nil {
		t.Fatalf("second submit on drive 0 should queue, not error: %v", err)
	}

	// Only one call should have reached the executor so far.
	if c := mock.callCount(); c != 1 {
		t.Errorf("expected 1 executor call while first job runs, got %d", c)
	}

	// Unblock to allow first job to finish and queue to drain.
	mock.release()
}

// immediateRipExecutor is a test double that returns immediately with no error.
type immediateRipExecutor struct{}

func (m *immediateRipExecutor) StartRip(_ context.Context, _ int, _ int, _ string, onEvent func(makemkv.Event)) error {
	if onEvent != nil {
		onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Current: 65536, Total: 65536, Max: 65536}})
	}
	return nil
}

func TestEngine_OnCompleteCallback(t *testing.T) {
	engine := NewEngine(&immediateRipExecutor{})

	var (
		cbMu      sync.Mutex
		cbJob     *Job
		cbErr     error
		cbCalled  bool
	)

	job := NewJob(0, 1, "DISC", "/output")
	job.OnComplete = func(j *Job, err error) {
		cbMu.Lock()
		defer cbMu.Unlock()
		cbJob = j
		cbErr = err
		cbCalled = true
	}

	if err := engine.Submit(job); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Wait for the job to complete and the callback to fire.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		cbMu.Lock()
		called := cbCalled
		cbMu.Unlock()
		if called {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cbMu.Lock()
	defer cbMu.Unlock()

	if !cbCalled {
		t.Fatal("OnComplete callback was never called")
	}
	if cbJob != job {
		t.Errorf("OnComplete received wrong job pointer: got %p, want %p", cbJob, job)
	}
	if cbErr != nil {
		t.Errorf("OnComplete received non-nil error: %v", cbErr)
	}
	if cbJob.Status != StatusCompleted {
		t.Errorf("job status = %q, want %q", cbJob.Status, StatusCompleted)
	}
}

func TestEngineAllowsConcurrentDrives(t *testing.T) {
	mock := newMockRipExecutor()
	engine := NewEngine(mock)

	job0 := NewJob(0, 1, "DISC_A", "/output")
	job1 := NewJob(1, 1, "DISC_B", "/output")

	if err := engine.Submit(job0); err != nil {
		t.Fatalf("submit on drive 0 should succeed, got: %v", err)
	}
	if err := engine.Submit(job1); err != nil {
		t.Fatalf("submit on drive 1 should succeed, got: %v", err)
	}

	// Give both goroutines time to enter StartRip.
	time.Sleep(20 * time.Millisecond)

	calls := mock.callCount()
	if calls != 2 {
		t.Fatalf("expected 2 StartRip calls for two drives, got %d", calls)
	}

	// Unblock to allow cleanup.
	mock.release()
}
