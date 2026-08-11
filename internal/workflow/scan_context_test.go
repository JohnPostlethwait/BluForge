package workflow

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// slowScanner mimics a disc that takes a long time to read — a damaged one
// retries every unreadable sector, which can run for minutes.
type slowScanner struct {
	mu      sync.Mutex
	calls   int
	ctxSeen context.Context
	started chan struct{}
	release chan struct{}
	scanned *makemkv.DiscScan
}

func newSlowScanner() *slowScanner {
	return &slowScanner{
		started: make(chan struct{}, 4),
		release: make(chan struct{}),
		scanned: &makemkv.DiscScan{
			DiscName:   "POLICE STORY 2 4K UHD",
			TitleCount: 1,
			Titles: []makemkv.TitleInfo{
				{Index: 0, Attributes: map[int]string{2: "Feature", 9: "2:01:53", 16: "00000.mpls"}},
			},
		},
	}
}

func (s *slowScanner) ScanDisc(ctx context.Context, _ int) (*makemkv.DiscScan, error) {
	s.mu.Lock()
	s.calls++
	s.ctxSeen = ctx
	s.mu.Unlock()

	s.started <- struct{}{}
	<-s.release
	return s.scanned, nil
}

func (s *slowScanner) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *slowScanner) context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctxSeen
}

// Observed in production: scanning a damaged disc died with "signal: killed".
// The scan ran on the HTTP request's context, so when the browser gave up
// waiting — which a multi-minute scan guarantees — exec.CommandContext killed
// makemkvcon mid-read. The same disc scans fine from a shell, which has no
// request attached.
func TestScanSurvivesCallerCancellation(t *testing.T) {
	scanner := newSlowScanner()
	orch := NewOrchestrator(OrchestratorDeps{Scanner: scanner})
	defer close(scanner.release)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _, _ = orch.ScanDisc(ctx, 0) }()

	select {
	case <-scanner.started:
	case <-time.After(asyncDeadline):
		t.Fatal("scan never started")
	}

	// The browser stops waiting.
	cancel()

	scanCtx := scanner.context()
	if scanCtx == nil {
		t.Fatal("no context captured")
	}
	select {
	case <-scanCtx.Done():
		t.Fatalf("scan context was cancelled with the caller: %v", scanCtx.Err())
	case <-time.After(100 * time.Millisecond):
		// Still alive, which is the point.
	}
}

// A user whose scan appears to hang will click again. That must not start a
// second makemkvcon against the same drive — they serialise on the executor
// mutex anyway, so the second would simply double the wait.
func TestConcurrentScansOfOneDriveRunOnce(t *testing.T) {
	scanner := newSlowScanner()
	orch := NewOrchestrator(OrchestratorDeps{Scanner: scanner})

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = orch.ScanDisc(context.Background(), 0)
		}()
	}

	select {
	case <-scanner.started:
	case <-time.After(asyncDeadline):
		t.Fatal("scan never started")
	}
	time.Sleep(200 * time.Millisecond)
	close(scanner.release)
	wg.Wait()

	if n := scanner.callCount(); n != 1 {
		t.Errorf("ran %d scans of one drive, want 1", n)
	}
}
