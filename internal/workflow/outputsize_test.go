package workflow

import (
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

// The GUI reported "completed · 67.4 GB" for a file that is 118 MB on disk,
// because the number was MakeMKV's estimate for the title and nothing ever
// looked at the result. Measuring is the only thing that makes the claim true.
func TestCompletedJobRecordsTheFileThatLanded(t *testing.T) {
	orch, store, outputDir := setupOrchestrator(t)

	// The estimate and the delivered file are deliberately different sizes: the
	// mock writes four bytes while the selection claims 1024, so a test that
	// passes cannot be reading the estimate back.
	const estimate = 1024
	const delivered = 4

	orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "MY_MOVIE_DISC",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles: []TitleSelection{{
			TitleIndex: 0,
			TitleName:  "Main Feature",
			SourceFile: "00000.mpls",
			SizeBytes:  estimate,
		}},
	})

	var job *db.RipJob
	deadline := time.Now().Add(asyncDeadline)
	for time.Now().Before(deadline) {
		jobs, err := store.ListJobsByStatus("completed")
		if err != nil {
			t.Fatalf("ListJobsByStatus: %v", err)
		}
		if len(jobs) > 0 {
			job = &jobs[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job == nil {
		t.Fatal("timed out waiting for the rip to complete")
	}

	if job.OutputSizeBytes != delivered {
		t.Errorf("OutputSizeBytes = %d, want %d (the file on disk)", job.OutputSizeBytes, delivered)
	}
	if job.SizeBytes != estimate {
		t.Errorf("SizeBytes = %d, want the estimate %d left intact", job.SizeBytes, estimate)
	}
}
