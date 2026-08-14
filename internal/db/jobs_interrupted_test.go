package db

import (
	"strings"
	"testing"
)

func jobWithStatus(t *testing.T, store *Store, disc, status string) int64 {
	t.Helper()
	id, err := store.CreateJob(RipJob{DiscName: disc, TitleName: "Feature", Status: status})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if status != "pending" {
		if err := store.UpdateJobStatus(id, status, 0, ""); err != nil {
			t.Fatalf("UpdateJobStatus: %v", err)
		}
	}
	return id
}

// A rip lives in the engine's memory; the queue behind it does too. Neither
// survives a restart, but their rows do — six attempts at one disc across six
// redeploys left six rows all claiming to be ripping it right now.
func TestInterruptedRipsAreClosedOut(t *testing.T) {
	store := openTestDB(t)

	ripping := jobWithStatus(t, store, "RAMBO_DISC2", "ripping")
	organizing := jobWithStatus(t, store, "RAMBO_DISC2", "organizing")
	pending := jobWithStatus(t, store, "RAMBO_DISC2", "pending")

	n, err := store.FailInterruptedJobs()
	if err != nil {
		t.Fatalf("FailInterruptedJobs: %v", err)
	}
	if n != 3 {
		t.Errorf("closed out %d jobs, want 3", n)
	}

	for _, id := range []int64{ripping, organizing, pending} {
		job, err := store.GetJob(id)
		if err != nil || job == nil {
			t.Fatalf("GetJob(%d): %v", id, err)
		}
		if job.Status != "failed" {
			t.Errorf("job %d is still %q after a restart", id, job.Status)
		}
	}
}

// The disc may have been perfectly readable. Blaming it sends the user to
// salvage a disc that never needed salvaging.
func TestAnInterruptedRipDoesNotBlameTheDisc(t *testing.T) {
	store := openTestDB(t)
	id := jobWithStatus(t, store, "RAMBO_DISC2", "ripping")

	if _, err := store.FailInterruptedJobs(); err != nil {
		t.Fatalf("FailInterruptedJobs: %v", err)
	}

	job, err := store.GetJob(id)
	if err != nil || job == nil {
		t.Fatalf("GetJob: %v", err)
	}
	msg := strings.ToLower(job.ErrorMessage)
	if !strings.Contains(msg, "interrupted") {
		t.Errorf("ErrorMessage = %q, want it to say the rip was interrupted", job.ErrorMessage)
	}
	if strings.Contains(msg, "saved no titles") || strings.Contains(msg, "could not read") {
		t.Errorf("ErrorMessage = %q, which reads as a disc fault", job.ErrorMessage)
	}
}

// Finished work is history and must be left exactly as it was.
func TestFinishedJobsSurviveARestart(t *testing.T) {
	store := openTestDB(t)

	completed := jobWithStatus(t, store, "DEADPOOL_2", "completed")
	failed := jobWithStatus(t, store, "DEADPOOL_2", "failed")

	if _, err := store.FailInterruptedJobs(); err != nil {
		t.Fatalf("FailInterruptedJobs: %v", err)
	}

	job, err := store.GetJob(completed)
	if err != nil || job == nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != "completed" {
		t.Errorf("a completed rip was reopened as %q", job.Status)
	}

	job, err = store.GetJob(failed)
	if err != nil || job == nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.ErrorMessage != "" {
		t.Errorf("an already-failed rip had its reason overwritten with %q", job.ErrorMessage)
	}
}

// A clean shutdown with nothing running must not report closing anything out.
func TestNothingInFlightClosesNothing(t *testing.T) {
	store := openTestDB(t)
	jobWithStatus(t, store, "DEADPOOL_2", "completed")

	n, err := store.FailInterruptedJobs()
	if err != nil {
		t.Fatalf("FailInterruptedJobs: %v", err)
	}
	if n != 0 {
		t.Errorf("closed out %d jobs when none were in flight", n)
	}
}
