package workflow

import (
	"os"
	"testing"

	"path/filepath"
	"syscall"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/fsutil"
)

// The film that reaches the library has to be writable by the share group, or
// the user cannot manage their own media over SMB. makemkvcon writes the .mkv
// itself and the move preserves the mode it chose, so without normalising, its
// choice is what ends up on the shelf.
func TestRippedFileLandsGroupWritable(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithRipExecutor(t, completedRipExecutor{})

	// Set the real process umask, not just fsutil's copy of it. Directories in
	// the library are created by organizer with os.MkdirAll(0o777), which the
	// kernel masks, while the normalising below uses the captured value -- this
	// is only a faithful test of a container running UMASK=0002 if both agree,
	// which is what CaptureUmask arranges in production.
	//
	// Safe to do process-wide here: no test in this package calls t.Parallel(),
	// and other packages run in their own processes.
	prevProcess := syscall.Umask(0o002)
	prevFsutil := fsutil.Umask()
	fsutil.CaptureUmask()
	t.Cleanup(func() {
		syscall.Umask(prevProcess)
		fsutil.SetUmask(prevFsutil)
	})

	orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "INVICTUS",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles: []TitleSelection{{
			TitleIndex: 0, TitleName: "Invictus", SourceFile: "title00.mkv",
			SizeBytes: 1024, ContentType: "movie",
		}},
	})

	waitForCompletedJob(t, store)

	jobs, err := store.ListJobsByStatus("completed")
	if err != nil {
		t.Fatalf("ListJobsByStatus: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 completed job, got %d", len(jobs))
	}
	assertGroupWritable(t, jobs[0])
}

func assertGroupWritable(t *testing.T, job db.RipJob) {
	t.Helper()

	info, err := os.Stat(job.OutputPath)
	if err != nil {
		t.Fatalf("stat ripped file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o664 {
		t.Errorf("ripped file %s = %04o, want 0664", job.OutputPath, got)
	}

	// The directory it landed in has to be group-writable too, or the file
	// inside it still cannot be removed over the share.
	dir, err := os.Stat(filepath.Dir(job.OutputPath))
	if err != nil {
		t.Fatalf("stat media dir: %v", err)
	}
	if got := dir.Mode().Perm(); got&0o070 != 0o070 {
		t.Errorf("media dir = %04o, want group rwx", got)
	}
}
