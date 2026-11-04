package db

import (
	"testing"
	"time"
)

// The copy interval is what a job's throughput is measured over. The job's
// own created_at and updated_at also cover queueing, analysis and the move.
func TestUpdateJobCopyTimes(t *testing.T) {
	store := openTestDB(t)

	id, err := store.CreateJob(makeTestJob())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	got, err := store.GetJob(id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if !got.CopyStartedAt.IsZero() || !got.CopyFinishedAt.IsZero() {
		t.Fatalf("a new job has copy times %v .. %v, want none", got.CopyStartedAt, got.CopyFinishedAt)
	}

	start := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	end := start.Add(25*time.Minute + 30*time.Second)
	if err := store.UpdateJobCopyTimes(id, start, end); err != nil {
		t.Fatalf("UpdateJobCopyTimes: %v", err)
	}

	got, err = store.GetJob(id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if !got.CopyStartedAt.Equal(start) {
		t.Errorf("CopyStartedAt = %v, want %v", got.CopyStartedAt, start)
	}
	if !got.CopyFinishedAt.Equal(end) {
		t.Errorf("CopyFinishedAt = %v, want %v", got.CopyFinishedAt, end)
	}

	jobs, err := store.ListAllJobs(10, 0)
	if err != nil {
		t.Fatalf("ListAllJobs: %v", err)
	}
	if len(jobs) != 1 || !jobs[0].CopyFinishedAt.Equal(end) {
		t.Errorf("ListAllJobs did not carry the copy times: %+v", jobs)
	}
}
