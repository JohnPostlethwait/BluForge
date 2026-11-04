package ripper

import (
	"math"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// The copy's start is when MakeMKV says it is saving, not when the job began.
// The analysis before it can take minutes and writes nothing.
func TestEnteringTheCopyRecordsWhenItStarted(t *testing.T) {
	job := NewJob(0, 0, "DISC", "/out")
	job.SetPhase(PhaseAnalyzing)
	if !job.Snapshot().CopyStartedAt.IsZero() {
		t.Fatal("analysis recorded a copy start")
	}

	before := time.Now()
	job.SetPhase(PhaseCopying)
	got := job.Snapshot().CopyStartedAt
	if got.Before(before) || got.After(time.Now()) {
		t.Errorf("CopyStartedAt = %v, want the moment the copy began", got)
	}
}

func TestCopyRateIsBytesCopiedOverTimeSpentCopying(t *testing.T) {
	job := NewJob(0, 0, "DISC", "/out")
	job.TrackMetadata.SizeBytes = 10_000_000_000
	job.SetPhase(PhaseCopying)
	// The copy began 100 s ago; the job itself started long before that.
	job.StartedAt = time.Now().Add(-time.Hour)
	job.CopyStartedAt = time.Now().Add(-100 * time.Second)

	job.UpdateProgress(10) // 1 GB of 10 GB

	got := job.Snapshot().CopyRate
	want := 1_000_000_000.0 / 100
	if math.Abs(got-want)/want > 0.01 {
		t.Errorf("CopyRate = %.0f B/s, want about %.0f B/s", got, want)
	}
}

// Analysis progress says nothing about bytes written.
func TestNoCopyRateDuringAnalysis(t *testing.T) {
	job := NewJob(0, 0, "DISC", "/out")
	job.TrackMetadata.SizeBytes = 10_000_000_000
	job.SetPhase(PhaseAnalyzing)
	job.UpdateProgress(50)
	if r := job.Snapshot().CopyRate; r != 0 {
		t.Errorf("CopyRate = %v during analysis, want 0", r)
	}
}

// A mean over a few megabytes swings wildly. Nothing until enough has moved.
func TestNoCopyRateBeforeEnoughHasBeenCopied(t *testing.T) {
	job := NewJob(0, 0, "DISC", "/out")
	job.TrackMetadata.SizeBytes = 10_000_000_000
	job.SetPhase(PhaseCopying)
	job.CopyStartedAt = time.Now().Add(-10 * time.Second)
	job.UpdateProgress(1) // 100 MB
	if r := job.Snapshot().CopyRate; r != 0 {
		t.Errorf("CopyRate = %v after 100 MB, want 0 until enough is copied", r)
	}
}

// Without a size there are no bytes to divide.
func TestNoCopyRateWithoutASize(t *testing.T) {
	job := NewJob(0, 0, "DISC", "/out")
	job.SetPhase(PhaseCopying)
	job.CopyStartedAt = time.Now().Add(-100 * time.Second)
	job.UpdateProgress(50)
	if r := job.Snapshot().CopyRate; r != 0 {
		t.Errorf("CopyRate = %v with no size, want 0", r)
	}
}

// A retried rip copies again from the start. The rate describes this copy.
func TestARestartedCopyStartsItsRateAgain(t *testing.T) {
	job := NewJob(0, 0, "DISC", "/out")
	job.TrackMetadata.SizeBytes = 10_000_000_000
	job.SetPhase(PhaseCopying)
	job.CopyStartedAt = time.Now().Add(-100 * time.Second)
	job.UpdateProgress(50)
	if job.Snapshot().CopyRate == 0 {
		t.Fatal("precondition: no rate on the first copy")
	}

	job.SetPhase(PhaseCopying)
	if r := job.Snapshot().CopyRate; r != 0 {
		t.Errorf("CopyRate = %v after the copy restarted, want 0", r)
	}
}

// The engine records when the copy finished, before the file is organized,
// so a slow move does not count as copy time.
func TestEngineRecordsTheCopyInterval(t *testing.T) {
	exec := &phasedExecutor{done: make(chan struct{})}
	exec.run = func(onEvent func(makemkv.Event)) {
		onEvent(makemkv.Event{Type: "MSG", Message: &makemkv.Message{
			Code: makemkv.MsgSavingTitles, Text: "Saving 1 titles into directory /out",
		}})
		onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Total: 100, Max: 100}})
	}

	var atOrganize Job
	settled := make(chan struct{})
	job := NewJob(0, 0, "DISC", t.TempDir())
	job.OnComplete = func(j *Job, _ error) error {
		atOrganize = j.Snapshot()
		close(settled)
		return nil
	}

	engine := NewEngine(exec)
	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-settled:
	case <-time.After(30 * time.Second):
		t.Fatal("the rip never finished")
	}

	if atOrganize.CopyStartedAt.IsZero() {
		t.Error("CopyStartedAt not recorded")
	}
	if atOrganize.CopyFinishedAt.IsZero() {
		t.Fatal("CopyFinishedAt not recorded by the time the file is organized")
	}
	if atOrganize.CopyFinishedAt.Before(atOrganize.CopyStartedAt) {
		t.Errorf("copy finished %v before it started %v", atOrganize.CopyFinishedAt, atOrganize.CopyStartedAt)
	}
}

// A copy that never began has no interval to report.
func TestEngineRecordsNoCopyFinishWithoutACopy(t *testing.T) {
	exec := &phasedExecutor{done: make(chan struct{})}
	var atOrganize Job
	settled := make(chan struct{})
	job := NewJob(0, 0, "DISC", t.TempDir())
	job.OnComplete = func(j *Job, _ error) error {
		atOrganize = j.Snapshot()
		close(settled)
		return nil
	}
	if err := NewEngine(exec).Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	select {
	case <-settled:
	case <-time.After(30 * time.Second):
		t.Fatal("the rip never finished")
	}
	if !atOrganize.CopyFinishedAt.IsZero() {
		t.Errorf("CopyFinishedAt = %v for a rip that never copied", atOrganize.CopyFinishedAt)
	}
}
