package ripper

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// movingExecutor reports that the title moved, as the guard does when the
// enumeration does not hold what it expected.
type movingExecutor struct {
	mu       sync.Mutex
	attempts []int
	// succeedAt is the index that rips cleanly; -1 means every attempt reports
	// a move.
	succeedAt int
	// reportIndex is the corrected index the guard points at; -1 means the
	// title is not in this pass at all.
	reportIndex int
}

func (m *movingExecutor) StartRip(_ context.Context, _ makemkv.Source, titleID int, expectSource string, _ string, _ func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	m.mu.Lock()
	m.attempts = append(m.attempts, titleID)
	m.mu.Unlock()

	if titleID == m.succeedAt {
		return nil
	}
	return &makemkv.TitleMovedError{
		Requested:    titleID,
		Expected:     expectSource,
		Found:        "00006.m2ts",
		CorrectIndex: m.reportIndex,
	}
}

func (m *movingExecutor) tried() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int(nil), m.attempts...)
}

// Kiki's Delivery Service, 2026-09-03. BluForge used to re-point a job at
// whichever index the guard believed the title had moved to. The guard was
// wrong — it had read an angle number as a title number — and so job 1715, a
// rip of title 3, ran as a rip of title 1 and delivered a different cut of the
// film under the name of the one that was asked for. Twice, on two jobs, with
// no failure reported.
//
// A corrected index is a guess derived from a filename match against a partial
// enumeration. It is never authority for copying something other than what was
// asked for.
func TestARipIsNeverRetriedAtADifferentIndex(t *testing.T) {
	exec := &movingExecutor{succeedAt: 1, reportIndex: 1}
	job := NewJob(0, 3, "KIKIS_DELIVERY_SERVICE_BD", "/output")
	job.SourceFile = "00200.mpls"

	err := ripOnce(context.Background(), exec, job, nil)

	if err == nil {
		t.Fatal("a rip whose title did not match reported success")
	}
	tried := exec.tried()
	if len(tried) != 1 {
		t.Fatalf("makemkvcon was invoked %d times, want 1: %v", len(tried), tried)
	}
	if tried[0] != 3 {
		t.Errorf("ripped title %d, want the 3 the job was created for", tried[0])
	}
	if got := job.Snapshot().TitleIndex; got != 3 {
		t.Errorf("the job's title index was rewritten to %d; it must stay 3", got)
	}
}

// The failure has to arrive as itself so the report can say what was seen
// where, rather than as a bare string that has lost the detail.
func TestAMovedTitleFailsAsATitleMovedError(t *testing.T) {
	exec := &movingExecutor{succeedAt: -1, reportIndex: 1}
	job := NewJob(0, 3, "DISC", "/output")
	job.SourceFile = "00200.mpls"

	err := ripOnce(context.Background(), exec, job, nil)

	var moved *makemkv.TitleMovedError
	if !errors.As(err, &moved) {
		t.Fatalf("error is %T, want *TitleMovedError", err)
	}
	if moved.Requested != 3 {
		t.Errorf("Requested = %d, want 3", moved.Requested)
	}
}

// A rip that matches what was asked for is untouched by any of this.
func TestARipThatMatchesRunsNormally(t *testing.T) {
	exec := &movingExecutor{succeedAt: 2, reportIndex: -1}
	job := NewJob(0, 2, "DISC", "/output")
	job.SourceFile = "00003.mpls"

	if err := ripOnce(context.Background(), exec, job, nil); err != nil {
		t.Fatalf("a correct rip was refused: %v", err)
	}
	if tried := exec.tried(); len(tried) != 1 || tried[0] != 2 {
		t.Errorf("attempts = %v, want exactly [2]", tried)
	}
}
