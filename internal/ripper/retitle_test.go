package ripper

import (
	"context"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// movingExecutor refuses the first index it is given, reporting that the title
// moved — exactly as makemkvcon's enumeration does after a title fails to read.
type movingExecutor struct {
	mu       sync.Mutex
	attempts []int
	// succeedAt is the index that rips cleanly; -1 means every attempt reports
	// a move, which is what a disc whose enumeration keeps shifting looks like.
	succeedAt int
	// reportIndex is the corrected index it points at; -1 means the title is
	// not in this pass at all.
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

// Failing here would be safe but useless: the title is on the disc and readable,
// it just has a different number this pass. Retrying at the corrected index is
// the difference between a rip that works and one that gives up.
func TestRipRetriesAtTheCorrectedIndex(t *testing.T) {
	exec := &movingExecutor{succeedAt: 3, reportIndex: 3}
	job := NewJob(0, 4, "POLICE STORY 2 4K UHD", "/out")
	job.SourceFile = "00000.mpls"

	err := ripWithRetry(context.Background(), exec, job, func(makemkv.Event) {})
	if err != nil {
		t.Fatalf("rip failed despite a known corrected index: %v", err)
	}

	tried := exec.tried()
	if len(tried) != 2 || tried[0] != 4 || tried[1] != 3 {
		t.Errorf("attempts = %v, want [4 3]", tried)
	}
	if job.TitleIndex != 3 {
		t.Errorf("job.TitleIndex = %d, want 3 so the record matches what was ripped", job.TitleIndex)
	}
}

// A title that is not in this pass has no corrected index to retry at. It must
// fail rather than rip whatever holds that number.
func TestRipDoesNotRetryWhenTheTitleIsAbsent(t *testing.T) {
	exec := &movingExecutor{succeedAt: -1, reportIndex: -1}
	job := NewJob(0, 0, "POLICE STORY 2 4K UHD", "/out")
	job.SourceFile = "00005.mpls"

	err := ripWithRetry(context.Background(), exec, job, func(makemkv.Event) {})
	if err == nil {
		t.Fatal("a rip of an absent title succeeded")
	}
	if n := len(exec.tried()); n != 1 {
		t.Errorf("made %d attempts, want 1 — there is nothing to retry at", n)
	}
}

// One retry, not a loop. An enumeration that keeps moving would otherwise spin
// through the disc for hours.
func TestRipRetriesOnlyOnce(t *testing.T) {
	// Always reports a move, and the corrected index never rips either.
	exec := &movingExecutor{succeedAt: -1, reportIndex: 3}
	job := NewJob(0, 4, "DISC", "/out")
	job.SourceFile = "00000.mpls"

	if err := ripWithRetry(context.Background(), exec, job, func(makemkv.Event) {}); err == nil {
		t.Fatal("expected a failure once the retry was spent")
	}
	if n := len(exec.tried()); n != 2 {
		t.Errorf("made %d attempts, want 2 (original plus one retry)", n)
	}
}

// An ordinary rip must be untouched by any of this.
func TestRipPassesThroughWhenNothingMoved(t *testing.T) {
	exec := &movingExecutor{succeedAt: 1, reportIndex: 1}
	job := NewJob(0, 1, "DISC", "/out")
	job.SourceFile = "00003.mpls"

	if err := ripWithRetry(context.Background(), exec, job, func(makemkv.Event) {}); err != nil {
		t.Fatalf("a correct rip was disturbed: %v", err)
	}
	if n := len(exec.tried()); n != 1 {
		t.Errorf("made %d attempts, want 1", n)
	}
}
