package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// blockingRipExecutor holds the first rip open until released, so a second
// title for the same drive is forced to sit in the engine's queue where it can
// be cancelled.
type blockingRipExecutor struct {
	started chan struct{}
	release chan struct{}
}

func (e *blockingRipExecutor) StartRip(ctx context.Context, _ makemkv.Source, _ int, _ string, outputDir string, onEvent func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	select {
	case e.started <- struct{}{}:
	default:
	}
	select {
	case <-e.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	_ = os.WriteFile(filepath.Join(outputDir, "title_t00.mkv"), []byte("fake"), 0o644)
	if onEvent != nil {
		onEvent(makemkv.Event{
			Type:     "PRGV",
			Progress: &makemkv.Progress{Current: 65536, Total: 65536, Max: 65536},
		})
	}
	return nil
}

// TestRemoveQueuedReleasesBatch covers the leak found in the orphaned-temp-dir
// audit: cancelling a queued job used to drop it from the queue without firing
// OnComplete, so ManualRip's WaitGroup never reached zero. The goroutine waiting
// on it blocked forever, the batch's .rip- directory was never removed, and the
// job's database row stayed at "ripping".
func TestRemoveQueuedReleasesBatch(t *testing.T) {
	exec := &blockingRipExecutor{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	orch, store, outputDir := setupOrchestratorWithRipExecutor(t, exec)

	// Two titles on one drive: the engine runs the first and queues the second.
	result := orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "MY_MOVIE_DISC",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles: []TitleSelection{
			{TitleIndex: 0, TitleName: "First", SourceFile: "title00.mkv", SizeBytes: 1024, ContentType: "movie"},
			{TitleIndex: 1, TitleName: "Second", SourceFile: "title01.mkv", SizeBytes: 1024, ContentType: "movie"},
		},
	})
	if len(result.Titles) != 2 {
		t.Fatalf("expected 2 title results, got %d", len(result.Titles))
	}

	select {
	case <-exec.started:
	case <-time.After(asyncDeadline):
		t.Fatal("first rip never started")
	}

	queued := orch.engine.QueuedJobs()
	if len(queued) != 1 {
		t.Fatalf("expected 1 queued job, got %d", len(queued))
	}
	queuedID := queued[0].ID

	if !orch.engine.RemoveQueued(queuedID) {
		t.Fatal("RemoveQueued returned false for a job that was queued")
	}

	// The cancelled job must be settled in the database rather than left at
	// "ripping" forever.
	waitFor(t, "cancelled job to be marked failed", func() bool {
		job, err := store.GetJob(queuedID)
		return err == nil && job.Status == "failed"
	})

	// Let the surviving rip finish; the batch is only complete once it does.
	close(exec.release)
	waitFor(t, "first job to complete", func() bool {
		jobs, err := store.ListJobsByStatus("completed")
		return err == nil && len(jobs) == 1
	})

	// With the batch settled, the parent .rip- directory must be gone. This is
	// what the leaked WaitGroup used to prevent.
	waitFor(t, "parent .rip- temp dir to be removed", func() bool {
		return len(ripParentTempDirs(t, outputDir)) == 0
	})
}

// ripParentTempDirs lists leftover .rip-* batch directories. The package's
// ripTempDirs looks one level deeper, at the per-title directories inside them.
func ripParentTempDirs(t *testing.T, outputDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".rip-") {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs
}
