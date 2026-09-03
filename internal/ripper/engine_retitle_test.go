package ripper

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// engineMovingExecutor always reports that the title moved, as the guard does
// when the enumeration does not hold what it expected at the index asked for.
type engineMovingExecutor struct {
	mu       sync.Mutex
	attempts []int
	// elsewhere is the index the guard claims the title is really at. Nothing
	// may act on it.
	elsewhere int
}

func (m *engineMovingExecutor) StartRip(_ context.Context, _ makemkv.Source, titleID int, expectSource string, _ string, _ func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	m.mu.Lock()
	m.attempts = append(m.attempts, titleID)
	m.mu.Unlock()

	return &makemkv.TitleMovedError{
		Requested: titleID, Expected: expectSource,
		Found: "", CorrectIndex: m.elsewhere,
	}
}

func (m *engineMovingExecutor) tried() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int(nil), m.attempts...)
}

// A job whose title does not match fails. It does not quietly become a rip of
// something else.
//
// This is the Kiki's Delivery Service incident in one assertion: the engine
// used to take the guard's corrected index and copy that instead, which
// delivered a different cut of the film under the requested title's name and
// reported success.
func TestEngineFailsAJobWhoseTitleDoesNotMatch(t *testing.T) {
	exec := &engineMovingExecutor{elsewhere: 1}
	engine := NewEngine(exec)

	job := NewJob(0, 3, "KIKIS_DELIVERY_SERVICE_BD", "/output")
	job.SourceFile = "00200.mpls"
	if err := engine.Submit(job); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && job.Snapshot().Status != StatusFailed {
		time.Sleep(5 * time.Millisecond)
	}

	snap := job.Snapshot()
	if snap.Status != StatusFailed {
		t.Fatalf("job status is %q, want failed", snap.Status)
	}
	if tried := exec.tried(); len(tried) != 1 || tried[0] != 3 {
		t.Errorf("attempts = %v, want exactly [3] — index 1 must never be copied", tried)
	}
	if snap.TitleIndex != 3 {
		t.Errorf("the job's title index became %d; it must stay 3", snap.TitleIndex)
	}
	if snap.Error == "" {
		t.Error("the job failed without saying why")
	}
}
