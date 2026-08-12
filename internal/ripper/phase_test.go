package ripper

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// phasedExecutor replays what makemkvcon does on a real rip: progress through a
// preliminary analysis phase, then "Saving N titles", then the copy.
type phasedExecutor struct {
	mu     sync.Mutex
	phases []string
	done   chan struct{}
	once   sync.Once
	run    func(onEvent func(makemkv.Event))
}

func (p *phasedExecutor) StartRip(_ context.Context, _ makemkv.Source, _ int, _ string, _ string, onEvent func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	if p.run != nil {
		p.run(onEvent)
	}
	p.once.Do(func() { close(p.done) })
	return nil
}

func (p *phasedExecutor) record(job *Job) {
	p.mu.Lock()
	p.phases = append(p.phases, job.CurrentPhase())
	p.mu.Unlock()
}

func (p *phasedExecutor) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.phases...)
}

// The Police Story 2 rip showed "4.7 GB / 67.4 GB" two minutes in, with nothing
// written: the figure is progress multiplied by the estimate, and makemkvcon
// reports progress through its analysis phase before it copies a byte. It read
// as real to both of us.
func TestJobIsAnalyzingUntilTheCopyBegins(t *testing.T) {
	var job *Job
	exec := &phasedExecutor{done: make(chan struct{})}

	exec.run = func(onEvent func(makemkv.Event)) {
		// Analysis: progress climbs, nothing is being written.
		onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Total: 50, Max: 100}})
		exec.record(job)

		onEvent(makemkv.Event{Type: "MSG", Message: &makemkv.Message{
			Code: 5014, Text: "Saving 1 titles into directory /out",
		}})
		exec.record(job)

		onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Total: 10, Max: 100}})
		exec.record(job)
	}

	engine := NewEngine(exec)
	job = NewJob(0, 4, "POLICE STORY 2 4K UHD", t.TempDir())
	job.SourceFile = "00000.mpls"
	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case <-exec.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the rip never finished")
	}

	phases := exec.seen()
	if len(phases) != 3 {
		t.Fatalf("recorded %d phases, want 3: %v", len(phases), phases)
	}
	if phases[0] != PhaseAnalyzing {
		t.Errorf("during analysis the phase was %q, want %q — this is where the fabricated byte count came from",
			phases[0], PhaseAnalyzing)
	}
	if phases[1] != PhaseCopying {
		t.Errorf("after the save message the phase was %q, want %q", phases[1], PhaseCopying)
	}
	if phases[2] != PhaseCopying {
		t.Errorf("the phase regressed to %q during the copy", phases[2])
	}
}

// v0.4.8 still showed "10.9 GB / 63.9 GB · 17%" seconds into a rip. A progress
// restart was treated as the copy beginning, but makemkvcon restarts progress
// repeatedly through its preliminary work -- once per sub-operation -- so the
// first of those flipped the phase and the fabricated byte count came straight
// back. Only the save message marks the copy.
func TestProgressRestartsDuringAnalysisDoNotStartTheCopy(t *testing.T) {
	var job *Job
	exec := &phasedExecutor{done: make(chan struct{})}

	exec.run = func(onEvent func(makemkv.Event)) {
		// Preliminary work: several sub-operations, each running to 100 and
		// restarting. None of this is copying.
		for _, pct := range []int{40, 100, 3, 90, 100, 8, 60} {
			onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Total: pct, Max: 100}})
		}
		exec.record(job)
	}

	engine := NewEngine(exec)
	job = NewJob(0, 0, "RAMBO_DISC2", t.TempDir())
	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	select {
	case <-exec.done:
	case <-time.After(30 * time.Second):
		t.Fatal("the rip never finished")
	}

	if phases := exec.seen(); phases[0] != PhaseAnalyzing {
		t.Errorf("phase after preliminary progress restarts was %q, want %q", phases[0], PhaseAnalyzing)
	}
}

// A job that has not started reports nothing rather than guessing.
func TestAFreshJobHasNoPhase(t *testing.T) {
	if p := NewJob(0, 0, "DISC", "/out").CurrentPhase(); p != "" {
		t.Errorf("a fresh job reports phase %q, want empty", p)
	}
}
