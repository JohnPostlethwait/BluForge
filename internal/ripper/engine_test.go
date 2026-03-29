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

func TestEngineRejectsSecondRipOnSameDrive(t *testing.T) {
	mock := newMockRipExecutor()
	engine := NewEngine(mock)

	job1 := NewJob(0, 1, "DISC", "/output")
	if err := engine.Submit(job1); err != nil {
		t.Fatalf("first submit on drive 0 should succeed, got: %v", err)
	}

	// Give the goroutine time to start and enter StartRip.
	time.Sleep(20 * time.Millisecond)

	job2 := NewJob(0, 2, "DISC", "/output")
	err := engine.Submit(job2)
	if err == nil {
		t.Fatal("second submit on drive 0 should return an error")
	}

	// Unblock to allow cleanup.
	mock.release()
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
