package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/internal/web"
)

// mockRipExecutor completes rips instantly by firing a 100% progress event.
type mockRipExecutor struct{}

func (m *mockRipExecutor) StartRip(_ context.Context, _ int, _ int, _ string, onEvent func(makemkv.Event)) error {
	if onEvent != nil {
		onEvent(makemkv.Event{
			Type:     "PRGV",
			Progress: &makemkv.Progress{Current: 65536, Total: 65536, Max: 65536},
		})
	}
	return nil
}

func setupOrchestrator(t *testing.T) (*Orchestrator, *db.Store, string) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	engine := ripper.NewEngine(&mockRipExecutor{})
	org := organizer.New(
		"Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})",
		"TV/{{.Show}}/Season {{.Season}}/{{.Show}} - S{{.Season}}E{{.Episode}} - {{.EpisodeTitle}}",
	)
	hub := web.NewSSEHub()

	orch := NewOrchestrator(OrchestratorDeps{
		Store:     store,
		Engine:    engine,
		Organizer: org,
		SSEHub:    hub,
	})

	// Create the output directory so disk space checks pass.
	outputDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}

	return orch, store, outputDir
}

func TestManualRip_Success(t *testing.T) {
	orch, store, outputDir := setupOrchestrator(t)

	params := ManualRipParams{
		DriveIndex:    0,
		DiscName:      "MY_MOVIE_DISC",
		DiscKey:       "abc123",
		OutputDir:     outputDir,
		MovieTemplate: "Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})",
		SeriesTemplate: "TV/{{.Show}}/Season {{.Season}}/{{.Show}} - S{{.Season}}E{{.Episode}} - {{.EpisodeTitle}}",
		DuplicateAction: "overwrite",
		MediaItemID:   "item-1",
		ReleaseID:     "rel-1",
		MediaTitle:    "Test Movie",
		MediaYear:     "2024",
		MediaType:     "movie",
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
	destPath := filepath.Join(outputDir, "Movies", "Test Movie (2024)", "Test Movie (2024).mkv")
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(destPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	params := ManualRipParams{
		DriveIndex:    0,
		DiscName:      "MY_MOVIE_DISC",
		DiscKey:       "abc123",
		OutputDir:     outputDir,
		MovieTemplate: "Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})",
		SeriesTemplate: "TV/{{.Show}}/Season {{.Season}}/{{.Show}} - S{{.Season}}E{{.Episode}} - {{.EpisodeTitle}}",
		DuplicateAction: "skip",
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
