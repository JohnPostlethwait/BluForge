package workflow

import (
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

func failedJob(disc, source string, age time.Duration) db.RipJob {
	return db.RipJob{
		DiscName:   disc,
		SourceFile: source,
		TitleName:  source,
		Status:     "failed",
		CreatedAt:  time.Now().Add(-age),
	}
}

// Every attempt on a disc leaves its failures behind. A salvage that took all
// of them re-ripped seven titles from a night of retries instead of the one the
// user had actually chosen.
func TestSalvageRipsOnlyTheLatestAttempt(t *testing.T) {
	jobs := []db.RipJob{
		// A night of earlier attempts.
		failedJob("RAMBO_DISC2", "00800.mpls", 6*time.Hour),
		failedJob("RAMBO_DISC2", "00801.mpls", 5*time.Hour),
		failedJob("RAMBO_DISC2", "00999.mpls", 4*time.Hour),
		// The attempt that matters: one title, moments ago.
		failedJob("RAMBO_DISC2", "00800.mpls", time.Minute),
	}

	batch := latestFailedBatch(jobs, "RAMBO_DISC2")

	if len(batch) != 1 {
		t.Fatalf("selected %d titles, want 1 — the latest attempt", len(batch))
	}
	if batch[0].SourceFile != "00800.mpls" {
		t.Errorf("selected %q, want the title chosen in the latest attempt", batch[0].SourceFile)
	}
}

// Several titles chosen together are one attempt and all belong.
func TestSalvageKeepsEveryTitleFromOneAttempt(t *testing.T) {
	jobs := []db.RipJob{
		failedJob("RAMBO_DISC2", "00800.mpls", time.Minute),
		failedJob("RAMBO_DISC2", "00801.mpls", time.Minute+2*time.Second),
		failedJob("RAMBO_DISC2", "00802.mpls", time.Minute+4*time.Second),
		failedJob("RAMBO_DISC2", "00999.mpls", 5*time.Hour),
	}

	if batch := latestFailedBatch(jobs, "RAMBO_DISC2"); len(batch) != 3 {
		t.Errorf("selected %d titles, want the 3 chosen together", len(batch))
	}
}

// A title retried within one attempt is still one title.
func TestSalvageDoesNotRipATitleTwice(t *testing.T) {
	jobs := []db.RipJob{
		failedJob("RAMBO_DISC2", "00800.mpls", time.Minute),
		failedJob("RAMBO_DISC2", "00800.mpls", time.Minute+time.Second),
		failedJob("RAMBO_DISC2", "00800.mpls", time.Minute+2*time.Second),
	}

	if batch := latestFailedBatch(jobs, "RAMBO_DISC2"); len(batch) != 1 {
		t.Errorf("selected %d entries for one title, want 1", len(batch))
	}
}

// Another disc's failures are never this disc's work.
func TestSalvageIgnoresOtherDiscs(t *testing.T) {
	jobs := []db.RipJob{
		failedJob("SOME_OTHER_DISC", "00001.mpls", time.Minute),
		failedJob("RAMBO_DISC2", "00800.mpls", time.Minute),
	}

	batch := latestFailedBatch(jobs, "RAMBO_DISC2")
	if len(batch) != 1 || batch[0].DiscName != "RAMBO_DISC2" {
		t.Errorf("selected %+v, want only this disc's failure", batch)
	}
}

func TestSalvageSelectsNothingForADiscWithNoFailures(t *testing.T) {
	if batch := latestFailedBatch(nil, "RAMBO_DISC2"); len(batch) != 0 {
		t.Errorf("selected %d titles from no failures", len(batch))
	}
}
