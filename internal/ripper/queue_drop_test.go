package ripper

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// Queued jobs name a disc that is no longer in the drive.
//
// Pull a disc during a ten-title batch and the nine still queued each started
// in turn, took the drive, failed, and wrote a failure row — nine of them,
// scrolling through Activity, for one deliberate act by the user.
//
// Worse than the noise: if a different disc went in, the queue is a list of
// titles chosen from the disc that left, and running them reads the new disc
// under the old disc's names. The submission path refuses a disc that has
// changed since the page was rendered; jobs already queued had no such guard.
func TestQueuedJobsForADriveCanBeDroppedTogether(t *testing.T) {
	mock := newMockRipExecutor()
	defer mock.release()
	engine := NewEngine(mock)

	// One running, three queued behind it, plus one on another drive.
	running := NewJob(0, 1, "DISC", t.TempDir())
	if err := engine.Submit(running); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitUntil(t, func() bool { return engine.IsActive(0) })

	var (
		mu       sync.Mutex
		settled  []int64
		reasons  []error
	)
	for i := int64(2); i <= 4; i++ {
		j := NewJob(0, int(i), "DISC", t.TempDir())
		j.ID = i
		j.OnComplete = func(job *Job, err error) error {
			mu.Lock()
			settled = append(settled, job.ID)
			reasons = append(reasons, err)
			mu.Unlock()
			return nil
		}
		if err := engine.Submit(j); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	other := NewJob(1, 9, "OTHER_DISC", t.TempDir())
	other.ID = 99
	if err := engine.Submit(other); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	waitUntil(t, func() bool { return engine.IsActive(1) })

	// A second job on the other drive, so that drive has a queue too.
	otherQueued := NewJob(1, 10, "OTHER_DISC", t.TempDir())
	otherQueued.ID = 100
	if err := engine.Submit(otherQueued); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	dropped := engine.RemoveQueuedForDrive(0)
	if dropped != 3 {
		t.Errorf("dropped %d jobs, want 3", dropped)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(settled) != 3 {
		t.Fatalf("%d jobs settled, want 3 — a dropped job must still be closed out, or "+
			"its database row sits at \"ripping\" and its backup claim is never released", len(settled))
	}
	for i, err := range reasons {
		if !errors.Is(err, ErrDiscRemoved) {
			t.Errorf("job %d settled with %v, want ErrDiscRemoved", settled[i], err)
		}
	}

	// The other drive is untouched, and so is the running job.
	if got := len(engine.QueuedJobs()); got != 1 {
		t.Errorf("%d jobs left queued overall, want 1 (the other drive's)", got)
	}
	if !engine.IsActive(0) {
		t.Error("the running job on drive 0 was cancelled; only the queue should be dropped")
	}
}

// Nothing queued is not an error.
func TestRemoveQueuedForDriveWithAnEmptyQueue(t *testing.T) {
	engine := NewEngine(newMockRipExecutor())
	if n := engine.RemoveQueuedForDrive(0); n != 0 {
		t.Errorf("dropped %d, want 0", n)
	}
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
