package ripper

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// sourceRecordingExecutor captures the source each rip was issued against.
type sourceRecordingExecutor struct {
	mu      sync.Mutex
	sources []makemkv.Source
	done    chan struct{}
}

func (s *sourceRecordingExecutor) StartRip(_ context.Context, src makemkv.Source, _ int, expectSource string, _ string, _ func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	s.mu.Lock()
	s.sources = append(s.sources, src)
	s.mu.Unlock()
	select {
	case s.done <- struct{}{}:
	default:
	}
	return nil
}

func (s *sourceRecordingExecutor) recorded() []makemkv.Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]makemkv.Source(nil), s.sources...)
}

// A job created the ordinary way rips from its drive.
func TestNewJobDefaultsToDiscSource(t *testing.T) {
	job := NewJob(2, 1, "DISC", "/tmp/out")
	if got, want := job.Source, makemkv.DiscSource(2); got != want {
		t.Errorf("Source = %v, want %v", got, want)
	}
}

// A recovered disc rips from the stripped backup folder, not the drive — which
// is the whole point: MakeMKV cannot open the drive for these discs.
func TestEngineRipsFromJobSource(t *testing.T) {
	exec := &sourceRecordingExecutor{done: make(chan struct{}, 1)}
	engine := NewEngine(exec)

	job := NewJob(0, 3, "DISC", "/tmp/out")
	job.Source = makemkv.FileSource("/output/.bluforge-scratch/disc-abc123")

	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case <-exec.done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the rip to start")
	}

	got := exec.recorded()
	if len(got) != 1 {
		t.Fatalf("recorded %d rips, want 1", len(got))
	}
	if got[0].Arg() != "file:/output/.bluforge-scratch/disc-abc123" {
		t.Errorf("rip issued against %q, want the backup folder", got[0].Arg())
	}
}
