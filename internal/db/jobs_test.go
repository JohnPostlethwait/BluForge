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

func TestUpdateJobOutput(t *testing.T) {
	store := openTestDB(t)

	id, err := store.CreateJob(makeTestJob())
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := store.UpdateJobOutput(id, "/final/movie.mkv"); err != nil {
		t.Fatalf("UpdateJobOutput: %v", err)
	}

	got, err := store.GetJob(id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if got.OutputPath != "/final/movie.mkv" {
		t.Errorf("OutputPath: want %q, got %q", "/final/movie.mkv", got.OutputPath)
	}
}

func TestListAllJobs_Pagination(t *testing.T) {
	store := openTestDB(t)

	for i := 0; i < 5; i++ {
		if _, err := store.CreateJob(makeTestJob()); err != nil {
			t.Fatalf("CreateJob %d: %v", i, err)
		}
	}

	page1, err := store.ListAllJobs(2, 0)
	if err != nil {
		t.Fatalf("ListAllJobs(2, 0): %v", err)
	}
	if len(page1) != 2 {
		t.Errorf("page1: want 2 jobs, got %d", len(page1))
	}

	page2, err := store.ListAllJobs(2, 2)
	if err != nil {
		t.Fatalf("ListAllJobs(2, 2): %v", err)
	}
	if len(page2) != 2 {
		t.Errorf("page2: want 2 jobs, got %d", len(page2))
	}

	page3, err := store.ListAllJobs(2, 4)
	if err != nil {
		t.Fatalf("ListAllJobs(2, 4): %v", err)
	}
	if len(page3) != 1 {
		t.Errorf("page3: want 1 job, got %d", len(page3))
	}
}

func TestListAllJobs_EmptyTable(t *testing.T) {
	store := openTestDB(t)

	jobs, err := store.ListAllJobs(10, 0)
	if err != nil {
		t.Fatalf("ListAllJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("want 0 jobs, got %d", len(jobs))
	}
}

func TestCountJobsCompletedToday(t *testing.T) {
	store := openTestDB(t)

	id1, err := store.CreateJob(makeTestJob())
	if err != nil {
		t.Fatalf("CreateJob 1: %v", err)
	}
	id2, err := store.CreateJob(makeTestJob())
	if err != nil {
		t.Fatalf("CreateJob 2: %v", err)
	}

	if err := store.UpdateJobStatus(id1, "completed", 100, ""); err != nil {
		t.Fatalf("UpdateJobStatus 1: %v", err)
	}
	if err := store.UpdateJobStatus(id2, "completed", 100, ""); err != nil {
		t.Fatalf("UpdateJobStatus 2: %v", err)
	}

	count, err := store.CountJobsCompletedToday()
	if err != nil {
		t.Fatalf("CountJobsCompletedToday: %v", err)
	}
	if count != 2 {
		t.Errorf("count: want 2, got %d", count)
	}
}

func TestCountJobsCompletedToday_NoJobs(t *testing.T) {
	store := openTestDB(t)

	count, err := store.CountJobsCompletedToday()
	if err != nil {
		t.Fatalf("CountJobsCompletedToday: %v", err)
	}
	if count != 0 {
		t.Errorf("count: want 0, got %d", count)
	}
}

func TestDeleteJobsExcept(t *testing.T) {
	store := openTestDB(t)

	id1, err := store.CreateJob(makeTestJob())
	if err != nil {
		t.Fatalf("CreateJob 1: %v", err)
	}
	id2, err := store.CreateJob(makeTestJob())
	if err != nil {
		t.Fatalf("CreateJob 2: %v", err)
	}
	id3, err := store.CreateJob(makeTestJob())
	if err != nil {
		t.Fatalf("CreateJob 3: %v", err)
	}

	if err := store.DeleteJobsExcept([]int64{id1, id2}); err != nil {
		t.Fatalf("DeleteJobsExcept: %v", err)
	}

	// id3 should be gone.
	got3, err := store.GetJob(id3)
	if err != nil {
		t.Fatalf("GetJob id3: %v", err)
	}
	if got3 != nil {
		t.Errorf("id3 should have been deleted, but still exists")
	}

	// id1 and id2 should remain.
	got1, err := store.GetJob(id1)
	if err != nil {
		t.Fatalf("GetJob id1: %v", err)
	}
	if got1 == nil {
		t.Errorf("id1 should still exist but was deleted")
	}

	got2, err := store.GetJob(id2)
	if err != nil {
		t.Fatalf("GetJob id2: %v", err)
	}
	if got2 == nil {
		t.Errorf("id2 should still exist but was deleted")
	}
}

func TestDeleteJobsExcept_EmptyExcludes(t *testing.T) {
	store := openTestDB(t)

	if _, err := store.CreateJob(makeTestJob()); err != nil {
		t.Fatalf("CreateJob 1: %v", err)
	}
	if _, err := store.CreateJob(makeTestJob()); err != nil {
		t.Fatalf("CreateJob 2: %v", err)
	}
	if _, err := store.CreateJob(makeTestJob()); err != nil {
		t.Fatalf("CreateJob 3: %v", err)
	}

	if err := store.DeleteJobsExcept(nil); err != nil {
		t.Fatalf("DeleteJobsExcept(nil): %v", err)
	}

	jobs, err := store.ListAllJobs(100, 0)
	if err != nil {
		t.Fatalf("ListAllJobs: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("want 0 jobs after DeleteJobsExcept(nil), got %d", len(jobs))
	}
}

func TestDeleteJobsByFilter_StatusOnly(t *testing.T) {
	store := openTestDB(t)

	completed := makeTestJob()
	completed.Status = "completed"
	idC, err := store.CreateJob(completed)
	if err != nil {
		t.Fatalf("CreateJob completed: %v", err)
	}

	failed := makeTestJob()
	failed.Status = "failed"
	idF, err := store.CreateJob(failed)
	if err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	if err := store.DeleteJobsByFilter("", "failed", nil); err != nil {
		t.Fatalf("DeleteJobsByFilter: %v", err)
	}

	got, err := store.GetJob(idC)
	if err != nil {
		t.Fatalf("GetJob completed: %v", err)
	}
	if got == nil {
		t.Error("completed job should still exist but was deleted")
	}

	got, err = store.GetJob(idF)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if got != nil {
		t.Error("failed job should have been deleted but still exists")
	}
}

func TestDeleteJobsByFilter_SearchOnly(t *testing.T) {
	store := openTestDB(t)

	batman := makeTestJob()
	batman.DiscName = "Batman Begins"
	batman.Status = "completed"
	idB, err := store.CreateJob(batman)
	if err != nil {
		t.Fatalf("CreateJob batman: %v", err)
	}

	superman := makeTestJob()
	superman.DiscName = "Superman Returns"
	superman.Status = "completed"
	idS, err := store.CreateJob(superman)
	if err != nil {
		t.Fatalf("CreateJob superman: %v", err)
	}

	if err := store.DeleteJobsByFilter("batman", "", nil); err != nil {
		t.Fatalf("DeleteJobsByFilter: %v", err)
	}

	got, err := store.GetJob(idB)
	if err != nil {
		t.Fatalf("GetJob batman: %v", err)
	}
	if got != nil {
		t.Error("batman job should have been deleted but still exists")
	}

	got, err = store.GetJob(idS)
	if err != nil {
		t.Fatalf("GetJob superman: %v", err)
	}
	if got == nil {
		t.Error("superman job should still exist but was deleted")
	}
}

func TestDeleteJobsByFilter_SearchAndStatus(t *testing.T) {
	store := openTestDB(t)

	j1 := makeTestJob()
	j1.DiscName = "Batman Begins"
	j1.Status = "completed"
	id1, err := store.CreateJob(j1)
	if err != nil {
		t.Fatalf("CreateJob j1: %v", err)
	}

	j2 := makeTestJob()
	j2.DiscName = "Batman Returns"
	j2.Status = "failed"
	id2, err := store.CreateJob(j2)
	if err != nil {
		t.Fatalf("CreateJob j2: %v", err)
	}

	j3 := makeTestJob()
	j3.DiscName = "Superman"
	j3.Status = "completed"
	id3, err := store.CreateJob(j3)
	if err != nil {
		t.Fatalf("CreateJob j3: %v", err)
	}

	if err := store.DeleteJobsByFilter("batman", "completed", nil); err != nil {
		t.Fatalf("DeleteJobsByFilter: %v", err)
	}

	got, err := store.GetJob(id1)
	if err != nil {
		t.Fatalf("GetJob j1: %v", err)
	}
	if got != nil {
		t.Error("j1 should have been deleted but still exists")
	}

	got, err = store.GetJob(id2)
	if err != nil {
		t.Fatalf("GetJob j2: %v", err)
	}
	if got == nil {
		t.Error("j2 should still exist but was deleted")
	}

	got, err = store.GetJob(id3)
	if err != nil {
		t.Fatalf("GetJob j3: %v", err)
	}
	if got == nil {
		t.Error("j3 should still exist but was deleted")
	}
}

func TestDeleteJobsByFilter_RespectsExcludeIDs(t *testing.T) {
	store := openTestDB(t)

	j1 := makeTestJob()
	j1.Status = "failed"
	id1, err := store.CreateJob(j1)
	if err != nil {
		t.Fatalf("CreateJob j1: %v", err)
	}

	j2 := makeTestJob()
	j2.Status = "failed"
	id2, err := store.CreateJob(j2)
	if err != nil {
		t.Fatalf("CreateJob j2: %v", err)
	}

	if err := store.DeleteJobsByFilter("", "failed", []int64{id1}); err != nil {
		t.Fatalf("DeleteJobsByFilter: %v", err)
	}

	got, err := store.GetJob(id1)
	if err != nil {
		t.Fatalf("GetJob id1: %v", err)
	}
	if got == nil {
		t.Error("id1 should still exist (excluded) but was deleted")
	}

	got, err = store.GetJob(id2)
	if err != nil {
		t.Fatalf("GetJob id2: %v", err)
	}
	if got != nil {
		t.Error("id2 should have been deleted but still exists")
	}
}

func TestDeleteJobsByFilter_EmptyFiltersReturnsError(t *testing.T) {
	store := openTestDB(t)

	if err := store.DeleteJobsByFilter("", "", nil); err == nil {
		t.Error("expected error when no filters provided, got nil")
	}

	if err := store.DeleteJobsByFilter("", "all", nil); err == nil {
		t.Error("expected error when status is 'all' and no other filters, got nil")
	}
}

func TestDeleteJobsByFilter_TitleNameSearch(t *testing.T) {
	store := openTestDB(t)

	j1 := makeTestJob()
	j1.TitleName = "Directors Cut"
	j1.Status = "completed"
	id1, err := store.CreateJob(j1)
	if err != nil {
		t.Fatalf("CreateJob j1: %v", err)
	}

	j2 := makeTestJob()
	j2.TitleName = "Theatrical Cut"
	j2.Status = "completed"
	id2, err := store.CreateJob(j2)
	if err != nil {
		t.Fatalf("CreateJob j2: %v", err)
	}

	if err := store.DeleteJobsByFilter("Directors", "", nil); err != nil {
		t.Fatalf("DeleteJobsByFilter: %v", err)
	}

	got, err := store.GetJob(id1)
	if err != nil {
		t.Fatalf("GetJob id1: %v", err)
	}
	if got != nil {
		t.Error("id1 (title matched) should have been deleted but still exists")
	}

	got, err = store.GetJob(id2)
	if err != nil {
		t.Fatalf("GetJob id2: %v", err)
	}
	if got == nil {
		t.Error("id2 (title didn't match) should still exist but was deleted")
	}
}

func TestCreateJob_TrackMetadata(t *testing.T) {
	store := openTestDB(t)
	meta := `{"SizeBytes":1000,"SizeHuman":"1 KB","Duration":"1:00:00","AudioTracks":[{"Language":"English","Codec":"TrueHD","Channels":"7.1"}],"SubtitleLanguages":["English"]}`
	id, err := store.CreateJob(RipJob{
		DriveIndex:    0,
		DiscName:      "DISC",
		TitleIndex:    1,
		TitleName:     "Feature",
		Status:        "pending",
		TrackMetadata: meta,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetJob(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.TrackMetadata != meta {
		t.Errorf("track_metadata: got %q, want %q", got.TrackMetadata, meta)
	}
}
