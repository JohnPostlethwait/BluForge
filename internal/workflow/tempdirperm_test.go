package workflow

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/fsutil"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// permCapturingExecutor records the permissions of the per-title temp dir and
// its parent .rip- dir at the moment the rip runs, which is the only point at
// which both are guaranteed to still exist.
type permCapturingExecutor struct {
	mu         sync.Mutex
	titleMode  os.FileMode
	parentMode os.FileMode
	captured   bool
}

func (e *permCapturingExecutor) StartRip(_ context.Context, _ makemkv.Source, _ int, _ string, outputDir string, onEvent func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	titleInfo, err := os.Stat(outputDir)
	if err != nil {
		return err
	}
	parentInfo, err := os.Stat(filepath.Dir(outputDir))
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.titleMode = titleInfo.Mode().Perm()
	e.parentMode = parentInfo.Mode().Perm()
	e.captured = true
	e.mu.Unlock()

	_ = os.WriteFile(filepath.Join(outputDir, "title_t00.mkv"), []byte("fake"), 0o644)
	if onEvent != nil {
		onEvent(makemkv.Event{
			Type:     "PRGV",
			Progress: &makemkv.Progress{Current: 65536, Total: 65536, Max: 65536},
		})
	}
	return nil
}

// TestManualRipTempDirsFollowUmask is the end-to-end guard for the reported
// bug: os.MkdirTemp's hardcoded 0o700 left .rip-* directories readable only by
// the container user, so an orphaned one could not be deleted from the share.
// Both the parent and the per-title directory must follow the umask like every
// other directory in the output tree.
func TestManualRipTempDirsFollowUmask(t *testing.T) {
	exec := &permCapturingExecutor{}
	orch, store, outputDir := setupOrchestratorWithRipExecutor(t, exec)

	// 0o002 is the shipped default: 0o775 directories, group-writable so the
	// share group can clean up after a crashed rip. fsutil.CaptureUmask is
	// deliberately not used: it mutates the process umask, which would race the
	// other tests in this package.
	prevUmask := fsutil.Umask()
	fsutil.SetUmask(0o002)
	t.Cleanup(func() { fsutil.SetUmask(prevUmask) })

	result := orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "MY_MOVIE_DISC",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles: []TitleSelection{{
			TitleIndex:  0,
			TitleName:   "Main Feature",
			SourceFile:  "title00.mkv",
			SizeBytes:   1024,
			ContentType: "movie",
		}},
	})
	if len(result.Titles) != 1 || result.Titles[0].Status != "submitted" {
		t.Fatalf("expected one submitted title, got %+v", result.Titles)
	}

	waitForCompletedJob(t, store)

	exec.mu.Lock()
	defer exec.mu.Unlock()
	if !exec.captured {
		t.Fatal("executor never ran, so no permissions were captured")
	}
	const want = os.FileMode(0o775)
	if exec.titleMode != want {
		t.Errorf("per-title temp dir mode = %04o, want %04o", exec.titleMode, want)
	}
	if exec.parentMode != want {
		t.Errorf(".rip- parent dir mode = %04o, want %04o", exec.parentMode, want)
	}
}
