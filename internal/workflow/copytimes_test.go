package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// copyingRipExecutor announces the copy the way makemkvcon does, then writes
// the file.
type copyingRipExecutor struct{}

func (copyingRipExecutor) StartRip(_ context.Context, _ makemkv.Source, _ int, _ string, outputDir string, onEvent func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	onEvent(makemkv.Event{Type: "MSG", Message: &makemkv.Message{
		Code: makemkv.MsgSavingTitles, Text: "Saving 1 titles into directory " + outputDir,
	}})
	_ = os.WriteFile(filepath.Join(outputDir, "title_t00.mkv"), []byte("fake"), 0o644)
	onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Total: 100, Max: 100}})
	return nil
}

// A finished job's throughput is its file over its copy, so the copy interval
// has to reach the database.
func TestCompletedJobRecordsItsCopyInterval(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithRipExecutor(t, copyingRipExecutor{})
	before := time.Now().Add(-time.Second)

	orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "MY_MOVIE_DISC",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		Titles: []TitleSelection{{
			TitleIndex: 0,
			TitleName:  "Main Feature",
			SourceFile: "00000.mpls",
			SizeBytes:  1024,
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

	if job.CopyStartedAt.IsZero() || job.CopyFinishedAt.IsZero() {
		t.Fatalf("copy interval not recorded: %v .. %v", job.CopyStartedAt, job.CopyFinishedAt)
	}
	if job.CopyStartedAt.Before(before) {
		t.Errorf("CopyStartedAt = %v, earlier than the rip", job.CopyStartedAt)
	}
	if job.CopyFinishedAt.Before(job.CopyStartedAt) {
		t.Errorf("copy finished %v before it started %v", job.CopyFinishedAt, job.CopyStartedAt)
	}
}
