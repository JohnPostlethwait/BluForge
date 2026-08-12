package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// findRipTemp returns the .rip-* directories left under outputDir.
func findRipTemp(t *testing.T, outputDir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(outputDir, ".rip-*", "t*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return matches
}

// A rip of Rambo ran forty minutes and read 48GB before the scratch stopped it.
// Whatever it had written was deleted unexamined -- the failure path called
// RemoveAll without ever looking inside. Even if it is only most of a film, that
// is the user's to decide about, not ours to discard silently.
func TestAFailedRipKeepsWhatItManagedToWrite(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithRipExecutor(t, &partialRipExecutor{bytes: 4096})

	orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "RAMBO_DISC2",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles:          []TitleSelection{{TitleIndex: 0, TitleName: "Feature", SourceFile: "00800.mpls"}},
	})

	job := waitForFailedJob(t, store)

	kept := findRipTemp(t, outputDir)
	if len(kept) == 0 {
		t.Fatal("the partial file was deleted; forty minutes of reading went with it")
	}

	// The user cannot act on something they are not told about.
	if !strings.Contains(job.ErrorMessage, ".rip-") {
		t.Errorf("the failure does not say where the partial was kept: %q", job.ErrorMessage)
	}
	// The original reason has to survive alongside it.
	if !strings.Contains(job.ErrorMessage, "saved no titles") {
		t.Errorf("the failure lost its reason: %q", job.ErrorMessage)
	}
}

// A rip that wrote nothing leaves nothing behind. Keeping empty directories
// would litter the output share for no benefit.
func TestAFailedRipThatWroteNothingCleansUp(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithRipExecutor(t, &partialRipExecutor{bytes: 0})

	orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "RAMBO_DISC2",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles:          []TitleSelection{{TitleIndex: 0, TitleName: "Feature", SourceFile: "00800.mpls"}},
	})

	job := waitForFailedJob(t, store)

	if kept := findRipTemp(t, outputDir); len(kept) != 0 {
		t.Errorf("an empty temp dir was left behind: %v", kept)
	}
	if strings.Contains(job.ErrorMessage, ".rip-") {
		t.Errorf("the failure claims a partial was kept when nothing was written: %q", job.ErrorMessage)
	}
}
