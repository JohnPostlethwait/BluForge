package ripper

import (
	"context"
	"testing"
)

func TestJobStatusTransitions(t *testing.T) {
	job := NewJob(0, 1, "DISC_NAME", "/output")

	// Initial state
	if job.Status != StatusPending {
		t.Fatalf("expected status %q, got %q", StatusPending, job.Status)
	}
	if !job.StartedAt.IsZero() {
		t.Error("StartedAt should be zero before Start()")
	}

	// Start
	job.Start()
	if job.Status != StatusRipping {
		t.Fatalf("expected status %q after Start(), got %q", StatusRipping, job.Status)
	}
	if job.StartedAt.IsZero() {
		t.Error("StartedAt should be set after Start()")
	}

	// UpdateProgress
	job.UpdateProgress(42)
	if job.Progress != 42 {
		t.Fatalf("expected progress 42, got %d", job.Progress)
	}

	// Complete
	job.Complete("/output/movie.mkv")
	if job.Status != StatusCompleted {
		t.Fatalf("expected status %q after Complete(), got %q", StatusCompleted, job.Status)
	}
	if job.Progress != 100 {
		t.Fatalf("expected progress 100 after Complete(), got %d", job.Progress)
	}
	if job.OutputPath != "/output/movie.mkv" {
		t.Fatalf("expected OutputPath %q, got %q", "/output/movie.mkv", job.OutputPath)
	}
	if job.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set after Complete()")
	}
}

func TestJobFail(t *testing.T) {
	job := NewJob(0, 1, "DISC_NAME", "/output")
	job.Start()
	job.Fail("disc read error")

	if job.Status != StatusFailed {
		t.Fatalf("expected status %q after Fail(), got %q", StatusFailed, job.Status)
	}
	if job.Error != "disc read error" {
		t.Fatalf("expected Error %q, got %q", "disc read error", job.Error)
	}
	if job.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set after Fail()")
	}
}

func TestJobSkip(t *testing.T) {
	job := NewJob(0, 1, "DISC", "/output")
	job.Skip()

	if job.Status != StatusSkipped {
		t.Fatalf("expected status %q after Skip(), got %q", StatusSkipped, job.Status)
	}
	if job.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set after Skip()")
	}
}

func TestJobCancel_WithCancelFunc(t *testing.T) {
	job := NewJob(0, 1, "DISC", "/output")
	ctx, cancel := context.WithCancel(context.Background())
	job.cancel = cancel

	job.Cancel()

	select {
	case <-ctx.Done():
		// expected: context was cancelled
	default:
		t.Fatal("expected context to be done after Cancel()")
	}
}

func TestJobCancel_NilCancelFunc(t *testing.T) {
	job := NewJob(0, 1, "DISC", "/output")
	// cancel is nil by default; calling Cancel() should not panic
	job.Cancel()
}
