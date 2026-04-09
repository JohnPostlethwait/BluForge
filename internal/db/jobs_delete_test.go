package db

import (
	"testing"
)

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
