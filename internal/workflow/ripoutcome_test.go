package workflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// blockDestination makes the directory a rip needs to write into impossible to
// create, by putting a regular file where it belongs. AtomicMove then fails
// for a reason that has nothing to do with the disc — a full or unwritable
// destination, which is the case this covers.
func blockDestination(t *testing.T, outputDir, mediaTitle string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(outputDir, mediaTitle), []byte("in the way"), 0o644); err != nil {
		t.Fatalf("blocking the destination: %v", err)
	}
}

func ripOneTitle(outputDir string) ManualRipParams {
	return ManualRipParams{
		DriveIndex:      0,
		DiscName:        "BLOCKED_DISC",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		MediaTitle:      "Blocked Movie",
		MediaType:       "movie",
		Titles: []TitleSelection{{
			TitleIndex:   0,
			TitleName:    "Main Feature",
			SourceFile:   "title_t00.mkv",
			SizeBytes:    1024,
			ContentType:  "movie",
			ContentTitle: "Blocked Movie",
		}},
	}
}

func waitForJobStatus(t *testing.T, orch *Orchestrator, status string) {
	t.Helper()
	deadline := time.Now().Add(asyncDeadline)
	for time.Now().Before(deadline) {
		jobs, err := orch.store.ListJobsByStatus(status)
		if err != nil {
			t.Fatalf("ListJobsByStatus: %v", err)
		}
		if len(jobs) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a job with status %q", status)
}

// A rip read the disc perfectly and then could not place the file. The disc is
// not the problem, so the repaired copy it was read from is exactly what a
// retry needs — and throwing it away costs tens of minutes and up to ~100GB to
// rebuild.
//
// The claim used to be released on the rip's own error, which is nil here, so
// the copy was deleted the moment the move failed.
func TestBackupSurvivesARipWhoseFileCouldNotBePlaced(t *testing.T) {
	orch, _, outputDir := setupOrchestrator(t)

	backupDir := backupFixture(t, "kept-after-failed-move")
	orch.registerRecovered(0, &RecoveredDisc{Dir: backupDir, Source: makemkv.FileSource(backupDir)})

	blockDestination(t, outputDir, "Blocked Movie")

	if res := orch.ManualRip(ripOneTitle(outputDir)); res.Titles[0].Status != "submitted" {
		t.Fatalf("title was not submitted: %+v", res.Titles[0])
	}
	waitForJobStatus(t, orch, "failed")

	if _, err := os.Stat(backupDir); err != nil {
		t.Errorf("the repaired copy was deleted although the rip did not produce a placed file: %v", err)
	}
}

// The complement: a rip that landed its file has no further use for the copy.
func TestBackupIsDiscardedWhenTheFileIsPlaced(t *testing.T) {
	orch, _, outputDir := setupOrchestrator(t)

	backupDir := backupFixture(t, "discarded-after-success")
	orch.registerRecovered(0, &RecoveredDisc{Dir: backupDir, Source: makemkv.FileSource(backupDir)})

	if res := orch.ManualRip(ripOneTitle(outputDir)); res.Titles[0].Status != "submitted" {
		t.Fatalf("title was not submitted: %+v", res.Titles[0])
	}
	waitForJobStatus(t, orch, "completed")

	deadline := time.Now().Add(asyncDeadline)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(backupDir); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("the repaired copy survived a rip that placed its file")
}
