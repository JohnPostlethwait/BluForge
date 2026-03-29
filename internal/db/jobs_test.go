package db

import (
	"testing"
)

func makeTestJob() RipJob {
	return RipJob{
		DriveIndex:  0,
		DiscName:    "Test Disc",
		TitleIndex:  1,
		TitleName:   "Main Feature",
		ContentType: "movie",
		OutputPath:  "",
		Status:      "pending",
		Progress:    0,
	}
}

func TestCreateAndGetJob(t *testing.T) {
	store := openTestDB(t)

	job := makeTestJob()
	id, err := store.CreateJob(job)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	got, err := store.GetJob(id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got == nil {
		t.Fatal("GetJob returned nil")
	}

	if got.ID != id {
		t.Errorf("ID: want %d, got %d", id, got.ID)
	}
	if got.DriveIndex != job.DriveIndex {
		t.Errorf("DriveIndex: want %d, got %d", job.DriveIndex, got.DriveIndex)
	}
	if got.DiscName != job.DiscName {
		t.Errorf("DiscName: want %q, got %q", job.DiscName, got.DiscName)
	}
	if got.TitleIndex != job.TitleIndex {
		t.Errorf("TitleIndex: want %d, got %d", job.TitleIndex, got.TitleIndex)
	}
	if got.TitleName != job.TitleName {
		t.Errorf("TitleName: want %q, got %q", job.TitleName, got.TitleName)
	}
	if got.ContentType != job.ContentType {
		t.Errorf("ContentType: want %q, got %q", job.ContentType, got.ContentType)
	}
	if got.Status != "pending" {
		t.Errorf("Status: want %q, got %q", "pending", got.Status)
	}
	if got.Progress != 0 {
		t.Errorf("Progress: want 0, got %d", got.Progress)
	}
}

func TestUpdateJobStatus(t *testing.T) {
	store := openTestDB(t)

	id, err := store.CreateJob(makeTestJob())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := store.UpdateJobStatus(id, "ripping", 50, ""); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	got, err := store.GetJob(id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if got.Status != "ripping" {
		t.Errorf("Status: want %q, got %q", "ripping", got.Status)
	}
	if got.Progress != 50 {
		t.Errorf("Progress: want 50, got %d", got.Progress)
	}
	if got.ErrorMessage != "" {
		t.Errorf("ErrorMessage: want empty, got %q", got.ErrorMessage)
	}
}

func TestListJobsByStatus(t *testing.T) {
	store := openTestDB(t)

	id1, err := store.CreateJob(makeTestJob())
	if err != nil {
		t.Fatalf("CreateJob 1: %v", err)
	}

	id2, err := store.CreateJob(makeTestJob())
	if err != nil {
		t.Fatalf("CreateJob 2: %v", err)
	}

	// Update job2 to "ripping".
	if err := store.UpdateJobStatus(id2, "ripping", 10, ""); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	pending, err := store.ListJobsByStatus("pending")
	if err != nil {
		t.Fatalf("ListJobsByStatus pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending jobs: want 1, got %d", len(pending))
	}
	if pending[0].ID != id1 {
		t.Errorf("pending job ID: want %d, got %d", id1, pending[0].ID)
	}

	ripping, err := store.ListJobsByStatus("ripping")
	if err != nil {
		t.Fatalf("ListJobsByStatus ripping: %v", err)
	}
	if len(ripping) != 1 {
		t.Fatalf("ripping jobs: want 1, got %d", len(ripping))
	}
	if ripping[0].ID != id2 {
		t.Errorf("ripping job ID: want %d, got %d", id2, ripping[0].ID)
	}
}
