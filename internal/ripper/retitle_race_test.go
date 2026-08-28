package ripper

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// movedThenBlockingExecutor reports the title has moved on its first call, then
// blocks on the retry so the test can read the job while the retry is in
// flight — which is exactly when the corrected index has just been written.
type movedThenBlockingExecutor struct {
	calls   int32
	inRetry chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *movedThenBlockingExecutor) StartRip(_ context.Context, _ makemkv.Source, titleID int, _ string, _ string, _ func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	if atomic.AddInt32(&m.calls, 1) == 1 {
		return &makemkv.TitleMovedError{Requested: titleID, CorrectIndex: titleID + 1, Found: "00006.m2ts"}
	}
	m.once.Do(func() { close(m.inRetry) })
	<-m.release
	return nil
}

// ripWithRetry writes job.TitleIndex directly when makemkvcon has renumbered
// the disc, with no lock — while Snapshot reads that field under the job's
// mutex on behalf of every page showing the queue. A writer that does not take
// the lock the readers take is a data race however rarely it lands.
//
// Fails under -race before the fix.
func TestCorrectingATitleIndexDoesNotRaceWithReaders(t *testing.T) {
	exec := &movedThenBlockingExecutor{
		inRetry: make(chan struct{}),
		release: make(chan struct{}),
	}
	engine := NewEngine(exec)

	done := make(chan struct{})
	job := NewJob(0, 4, "DISC", t.TempDir())
	job.SourceFile = "00000.mpls"
	job.OnComplete = func(_ *Job, _ error) error { close(done); return nil }

	// Readers running throughout, as the activity and dashboard pages are.
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = job.Snapshot().TitleIndex
			}
		}()
	}

	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case <-exec.inRetry:
	case <-time.After(2 * time.Second):
		close(stop)
		wg.Wait()
		t.Fatal("timed out waiting for the retry")
	}

	close(exec.release)
	<-done
	close(stop)
	wg.Wait()

	if got := job.Snapshot().TitleIndex; got != 5 {
		t.Errorf("TitleIndex = %d, want 5 — the corrected index was not recorded", got)
	}
}
