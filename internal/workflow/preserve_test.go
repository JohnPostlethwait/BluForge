package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// partialRipExecutor writes some of a title and then fails, as a rip does when
// it reaches a scratch 48GB into a 64GB stream.
type partialRipExecutor struct {
	bytes int
}

func (p *partialRipExecutor) StartRip(_ context.Context, _ makemkv.Source, _ int, _ string, outputDir string, _ func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	if p.bytes > 0 {
		_ = os.WriteFile(filepath.Join(outputDir, "title_t00.mkv"), make([]byte, p.bytes), 0o644)
	}
	return errors.New("makemkvcon saved no titles — the drive could not read it")
}

func waitForFailedJob(t *testing.T, store *db.Store) *db.RipJob {
	t.Helper()
	deadline := time.Now().Add(asyncDeadline)
	for time.Now().Before(deadline) {
		jobs, err := store.ListJobsByStatus("failed")
		if err != nil {
			t.Fatalf("ListJobsByStatus: %v", err)
		}
		if len(jobs) > 0 {
			return &jobs[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the rip to fail")
	return nil
}

func ripTempDirs(t *testing.T, outputDir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(outputDir, ".rip-*", "t*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return matches
}

func runFailingRip(t *testing.T, bytes int) (*db.RipJob, string) {
	t.Helper()
	orch, store, outputDir := setupOrchestratorWithRipExecutor(t, &partialRipExecutor{bytes: bytes})

	orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "RAMBO_DISC2",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles:          []TitleSelection{{TitleIndex: 0, TitleName: "Feature", SourceFile: "00800.mpls"}},
	})

	return waitForFailedJob(t, store), outputDir
}

// A partial MKV cannot be fed back into a rip or a salvage -- it is on the far
// side of the demux, and MakeMKV has no resume -- so keeping tens of gigabytes
// of it buys nothing. It is discarded, but only after being looked at.
func TestAFailedRipDiscardsItsPartialOutput(t *testing.T) {
	_, outputDir := runFailingRip(t, 4096)

	if left := ripTempDirs(t, outputDir); len(left) != 0 {
		t.Errorf("temp dirs survived a failed rip: %v", left)
	}
}

// The failure the user reads is the reason it failed, not an inventory of
// temporary files that no longer exist.
func TestAFailedRipReportsOnlyTheReason(t *testing.T) {
	job, _ := runFailingRip(t, 4096)

	if job.ErrorMessage != "makemkvcon saved no titles — the drive could not read it" {
		t.Errorf("ErrorMessage = %q, want the bare reason", job.ErrorMessage)
	}
}

// A rip that wrote nothing leaves nothing behind either.
func TestAFailedRipThatWroteNothingCleansUp(t *testing.T) {
	_, outputDir := runFailingRip(t, 0)

	if left := ripTempDirs(t, outputDir); len(left) != 0 {
		t.Errorf("an empty temp dir was left behind: %v", left)
	}
}

// largestFile is what makes the log line possible: it has to find the partial
// before the directory goes, and say nothing when there is nothing to say.
func TestLargestFileFindsTheBiggestPartial(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "small.mkv"), make([]byte, 10), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.mkv"), make([]byte, 4096), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	path, size := largestFile(dir)
	if filepath.Base(path) != "big.mkv" || size != 4096 {
		t.Errorf("largestFile = (%q, %d), want big.mkv at 4096", path, size)
	}
}

func TestLargestFileIgnoresAnEmptyOrMissingDir(t *testing.T) {
	if path, _ := largestFile(t.TempDir()); path != "" {
		t.Errorf("largestFile on an empty dir = %q, want empty", path)
	}
	if path, _ := largestFile(filepath.Join(t.TempDir(), "gone")); path != "" {
		t.Errorf("largestFile on a missing dir = %q, want empty", path)
	}
	// A zero-byte file is not a partial worth reporting.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "empty.mkv"), nil, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if path, _ := largestFile(dir); path != "" {
		t.Errorf("largestFile reported a zero-byte file: %q", path)
	}
}
