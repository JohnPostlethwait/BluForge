package ripper

import (
	"testing"
	"time"
)

// A physical drive change cancels by drive, not by job id — the event knows
// which drive lost its disc, not which job happened to be running on it.
func TestEngine_CancelActiveForDrive(t *testing.T) {
	exec := newBlockingRipExecutor()
	engine := NewEngine(exec)

	job := NewJob(0, 1, "DISC", t.TempDir())
	job.ID = 1
	done := make(chan struct{})
	job.OnComplete = func(_ *Job, _ error) error { close(done); return nil }

	if err := engine.Submit(job); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	exec.waitForStarted(t, 1)

	if !engine.CancelActiveForDrive(0) {
		t.Error("CancelActiveForDrive(0) = false, want true")
	}

	select {
	case <-done:
		// Context cancellation unblocked the executor, as intended.
	case <-time.After(2 * time.Second):
		t.Fatal("job did not complete after CancelActiveForDrive")
	}

	if engine.CancelActiveForDrive(5) {
		t.Error("CancelActiveForDrive(5) = true, want false (no rip on that drive)")
	}
}
