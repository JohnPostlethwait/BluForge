package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

func TestManualRip_Success(t *testing.T) {
	orch, store, outputDir := setupOrchestrator(t)

	params := ManualRipParams{
		DriveIndex:      0,
		DiscName:        "MY_MOVIE_DISC",
		DiscKey:         "abc123",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		MediaItemID:     "item-1",
		ReleaseID:       "rel-1",
		MediaTitle:      "Test Movie",
		MediaYear:       "2024",
		MediaType:       "movie",
		Titles: []TitleSelection{
			{
				TitleIndex:   0,
				TitleName:    "Main Feature",
				SourceFile:   "title00.mkv",
				SizeBytes:    1024,
				ContentType:  "movie",
				ContentTitle: "Test Movie",
				Year:         "2024",
			},
		},
	}

	result := orch.ManualRip(params)

	if len(result.Titles) != 1 {
		t.Fatalf("expected 1 title result, got %d", len(result.Titles))
	}
	if result.Titles[0].Status != "submitted" {
		t.Errorf("expected status 'submitted', got %q", result.Titles[0].Status)
	}

	// Wait for the async rip to complete (the mock executor is instant).
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	var job *db.RipJob
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for job to complete")
		case <-tick.C:
			jobs, err := store.ListJobsByStatus("completed")
			if err != nil {
				t.Fatalf("ListJobsByStatus: %v", err)
			}
			if len(jobs) > 0 {
				job = &jobs[0]
				goto done
			}
		}
	}
done:

	if job == nil {
		t.Fatal("no completed job found")
	}
	if job.DiscName != "MY_MOVIE_DISC" {
		t.Errorf("expected disc name 'MY_MOVIE_DISC', got %q", job.DiscName)
	}
	if job.TitleIndex != 0 {
		t.Errorf("expected title index 0, got %d", job.TitleIndex)
	}
	if job.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", job.Status)
	}
	if job.OutputPath == "" {
		t.Error("expected output path to be set")
	}

	// Verify disc mapping was saved.
	mapping, err := store.GetMapping("abc123")
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if mapping == nil {
		t.Fatal("expected disc mapping to be saved")
	}
	if mapping.MediaItemID != "item-1" {
		t.Errorf("expected MediaItemID 'item-1', got %q", mapping.MediaItemID)
	}
	if mapping.MediaTitle != "Test Movie" {
		t.Errorf("expected MediaTitle 'Test Movie', got %q", mapping.MediaTitle)
	}
}

func TestManualRip_DuplicateSkip(t *testing.T) {
	orch, _, outputDir := setupOrchestrator(t)

	// Pre-create the destination file so the duplicate check triggers.
	// New path: <MediaTitle>/<TitleName>.mkv
	destPath := filepath.Join(outputDir, "Test Movie", "Main Feature.mkv")
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(destPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	params := ManualRipParams{
		DriveIndex:      0,
		DiscName:        "MY_MOVIE_DISC",
		DiscKey:         "abc123",
		OutputDir:       outputDir,
		DuplicateAction: "skip",
		MediaTitle:      "Test Movie",
		Titles: []TitleSelection{
			{
				TitleIndex:   0,
				TitleName:    "Main Feature",
				SourceFile:   "title00.mkv",
				SizeBytes:    1024,
				ContentType:  "movie",
				ContentTitle: "Test Movie",
				Year:         "2024",
			},
		},
	}

	result := orch.ManualRip(params)

	if len(result.Titles) != 1 {
		t.Fatalf("expected 1 title result, got %d", len(result.Titles))
	}
	if result.Titles[0].Status != "skipped" {
		t.Errorf("expected status 'skipped', got %q", result.Titles[0].Status)
	}
	if result.Titles[0].Reason == "" {
		t.Error("expected a reason for the skip")
	}
}

func TestManualRip_DuplicateRename(t *testing.T) {
	orch, _, outputDir := setupOrchestrator(t)

	// Pre-create the destination file so the duplicate check triggers.
	destPath := filepath.Join(outputDir, "Test Movie", "Main Feature.mkv")
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(destPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	params := ManualRipParams{
		DriveIndex:      0,
		DiscName:        "MY_MOVIE_DISC",
		DiscKey:         "abc123",
		OutputDir:       outputDir,
		DuplicateAction: "rename",
		MediaTitle:      "Test Movie",
		Titles: []TitleSelection{
			{
				TitleIndex:   0,
				TitleName:    "Main Feature",
				SourceFile:   "title00.mkv",
				SizeBytes:    1024,
				ContentType:  "movie",
				ContentTitle: "Test Movie",
				Year:         "2024",
			},
		},
	}

	result := orch.ManualRip(params)

	if len(result.Titles) != 1 {
		t.Fatalf("expected 1 title result, got %d", len(result.Titles))
	}
	if result.Titles[0].Status != "submitted" {
		t.Errorf("expected status 'submitted', got %q", result.Titles[0].Status)
	}

	// Wait for the async rip to complete (the mock executor is instant).
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for job to complete")
		case <-tick.C:
			// Check if the renamed file exists
			renamedPath := filepath.Join(outputDir, "Test Movie", "Main Feature (1).mkv")
			if _, err := os.Stat(renamedPath); err == nil {
				// File found, we're done
				goto done
			}
		}
	}
done:

	// The original file must be untouched.
	orig, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("original file missing: %v", err)
	}
	if string(orig) != "existing" {
		t.Errorf("original file content changed: %q", orig)
	}

	// The ripped file must land at the (1) path.
	renamedPath := filepath.Join(outputDir, "Test Movie", "Main Feature (1).mkv")
	if _, err := os.Stat(renamedPath); err != nil {
		t.Errorf("renamed file not found at %q: %v", renamedPath, err)
	}
}

func TestManualRip_PathConfinementRejected(t *testing.T) {
	orch, _, outputDir := setupOrchestrator(t)

	// A MediaTitle of ".." survives SanitizeFilename (dots are not stripped),
	// causing filepath.Join(outputDir, "../title.mkv") to escape the output dir.
	params := ManualRipParams{
		DriveIndex:      0,
		DiscName:        "..",
		DiscKey:         "abc",
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
		MediaTitle:      "..",
		MediaYear:       "2024",
		MediaType:       "movie",
		Titles: []TitleSelection{
			{
				TitleIndex:   0,
				TitleName:    "title",
				SourceFile:   "title00.mkv",
				SizeBytes:    1024,
				ContentType:  "movie",
				ContentTitle: "..",
				Year:         "2024",
			},
		},
	}

	result := orch.ManualRip(params)

	if len(result.Titles) != 1 {
		t.Fatalf("expected 1 title result, got %d", len(result.Titles))
	}
	if result.Titles[0].Status != "failed" {
		t.Errorf("expected status 'failed', got %q", result.Titles[0].Status)
	}
	if !strings.Contains(result.Titles[0].Reason, "escapes output directory") {
		t.Errorf("expected path confinement error, got %q", result.Titles[0].Reason)
	}
}
