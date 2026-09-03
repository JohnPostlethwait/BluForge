package ripper

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// engineMovingExecutor reports a moved title on the first attempt, as the disc
// does when its enumeration shifts between invocations.
type engineMovingExecutor struct {
	mu       sync.Mutex
	attempts []int
	goodAt   int
	done     chan struct{}
	once     sync.Once
}

func (m *engineMovingExecutor) StartRip(_ context.Context, _ makemkv.Source, titleID int, expectSource string, _ string, _ func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	m.mu.Lock()
	m.attempts = append(m.attempts, titleID)
	m.mu.Unlock()

	if titleID == m.goodAt {
		m.once.Do(func() { close(m.done) })
		return nil
	}
	return &makemkv.TitleMovedError{
		Requested: titleID, Expected: expectSource,
		Found: "00006.m2ts", CorrectIndex: m.goodAt,
	}
}

func (m *engineMovingExecutor) tried() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int(nil), m.attempts...)
}

// ripWithRetry is only worth anything if the engine actually calls it. Without
// this, the retry could be correct and unreachable, and the disc would rip the
// wrong title exactly as before.
func TestEngineCorrectsAMovedTitle(t *testing.T) {
	exec := &engineMovingExecutor{goodAt: 3, done: make(chan struct{})}
	engine := NewEngine(exec)

	job := NewJob(0, 4, "POLICE STORY 2 4K UHD", t.TempDir())
	job.SourceFile = "00000.mpls"
	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case <-exec.done:
	case <-time.After(30 * time.Second):
		t.Fatalf("the engine never reached the corrected index; tried %v", exec.tried())
	}

	tried := exec.tried()
	if len(tried) != 2 || tried[0] != 4 || tried[1] != 3 {
		t.Errorf("attempts = %v, want [4 3]", tried)
	}
}
