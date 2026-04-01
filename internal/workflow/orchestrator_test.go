package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// mockRipExecutor completes rips instantly by firing a 100% progress event
// and creating a dummy .mkv file in the output directory (simulating MakeMKV).
type mockRipExecutor struct{}

func (m *mockRipExecutor) StartRip(_ context.Context, _ int, _ int, outputDir string, onEvent func(makemkv.Event)) error {
	// Simulate MakeMKV writing a .mkv file.
	_ = os.WriteFile(filepath.Join(outputDir, "title_t00.mkv"), []byte("fake"), 0o644)
	if onEvent != nil {
		onEvent(makemkv.Event{
			Type:     "PRGV",
			Progress: &makemkv.Progress{Current: 65536, Total: 65536, Max: 65536},
		})
	}
	return nil
}

// mockDriveExecutor implements DiscScanner for testing.
type mockDriveExecutor struct{}

func (m *mockDriveExecutor) ScanDisc(_ context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	return &makemkv.DiscScan{
		DriveIndex: driveIndex,
		DiscName:   "DEADPOOL_2",
		TitleCount: 1,
		Titles: []makemkv.TitleInfo{
			{
				Index: 0,
				Attributes: map[int]string{
					2:  "Deadpool 2",
					9:  "1:59:45",
					11: "1024",
					27: "title_t00.mkv",
					33: "00001.mpls",
				},
			},
		},
	}, nil
}

func setupOrchestrator(t *testing.T) (*Orchestrator, *db.Store, string) {
	t.Helper()
	return setupOrchestratorWithScanner(t, nil)
}

func setupOrchestratorWithScanner(t *testing.T, scanner DiscScanner) (*Orchestrator, *db.Store, string) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	engine := ripper.NewEngine(&mockRipExecutor{})
	org := organizer.New()
	orch := NewOrchestrator(OrchestratorDeps{
		Store:       store,
		Engine:      engine,
		Organizer:   org,
		OnBroadcast: func(string, string) {},
		Scanner:     scanner,
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

func TestAutoRip_WithMapping(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, outputDir := setupOrchestratorWithScanner(t, scanner)

	scan, _ := scanner.ScanDisc(context.Background(), 0)
	discKey := discdb.BuildDiscKey(scan)

	err := store.SaveMapping(db.DiscMapping{
		DiscKey:     discKey,
		DiscName:    "DEADPOOL_2",
		MediaItemID: "item-dp2",
		ReleaseID:   "rel-dp2",
		MediaTitle:  "Deadpool 2",
		MediaYear:   "2018",
		MediaType:   "movie",
	})
	if err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}

	cfg := AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
	}

	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip: %v", err)
	}

	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

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
				if jobs[0].DiscName != "DEADPOOL_2" {
					t.Errorf("expected disc name 'DEADPOOL_2', got %q", jobs[0].DiscName)
				}
				return
			}
		}
	}
}

func TestAutoRip_NoMatch_UsesDiscName(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, outputDir := setupOrchestratorWithScanner(t, scanner)

	// No mapping saved, no discdb client — should use disc name as directory.
	cfg := AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
	}

	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip: %v", err)
	}

	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

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
				if jobs[0].DiscName != "DEADPOOL_2" {
					t.Errorf("expected disc name 'DEADPOOL_2', got %q", jobs[0].DiscName)
				}
				if jobs[0].OutputPath == "" {
					t.Error("expected output path to be set")
				}
				return
			}
		}
	}
}

func TestRescan(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, _ := setupOrchestratorWithScanner(t, scanner)

	scan, _ := scanner.ScanDisc(context.Background(), 0)
	discKey := discdb.BuildDiscKey(scan)

	err := store.SaveMapping(db.DiscMapping{
		DiscKey:     discKey,
		DiscName:    "DEADPOOL_2",
		MediaItemID: "item-dp2",
		ReleaseID:   "rel-dp2",
		MediaTitle:  "Deadpool 2",
		MediaYear:   "2018",
		MediaType:   "movie",
	})
	if err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}

	mapping, err := store.GetMapping(discKey)
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if mapping == nil {
		t.Fatal("expected mapping to exist before rescan")
	}

	if err := orch.Rescan(context.Background(), 0); err != nil {
		t.Fatalf("Rescan: %v", err)
	}

	mapping, err = store.GetMapping(discKey)
	if err != nil {
		t.Fatalf("GetMapping after rescan: %v", err)
	}
	if mapping != nil {
		t.Error("expected mapping to be deleted after rescan")
	}
}

func TestScanDisc_CachesResult(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("ScanDisc: %v", err)
	}

	cached := orch.CachedScan(0, "DEADPOOL_2")
	if cached == nil {
		t.Fatal("expected cached scan to be non-nil")
	}
	if cached.DiscName != "DEADPOOL_2" {
		t.Errorf("expected DiscName 'DEADPOOL_2', got %q", cached.DiscName)
	}
}

func TestCachedScan_Miss(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	cached := orch.CachedScan(5, "NONEXISTENT")
	if cached != nil {
		t.Errorf("expected nil for cache miss, got %+v", cached)
	}
}

func TestGetCachedScanByDrive(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("ScanDisc: %v", err)
	}

	cached := orch.GetCachedScanByDrive(0)
	if cached == nil {
		t.Fatal("expected cached scan to be non-nil")
	}
	if cached.DiscName != "DEADPOOL_2" {
		t.Errorf("expected DiscName 'DEADPOOL_2', got %q", cached.DiscName)
	}
}

func TestGetCachedScanByDrive_NilWhenEmpty(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	cached := orch.GetCachedScanByDrive(0)
	if cached != nil {
		t.Errorf("expected nil when no scan cached, got %+v", cached)
	}
}

func TestInvalidateScan_ClearsCache(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("ScanDisc: %v", err)
	}

	orch.InvalidateScan(0)

	if cached := orch.CachedScan(0, "DEADPOOL_2"); cached != nil {
		t.Errorf("expected CachedScan to return nil after invalidation, got %+v", cached)
	}
	if cached := orch.GetCachedScanByDrive(0); cached != nil {
		t.Errorf("expected GetCachedScanByDrive to return nil after invalidation, got %+v", cached)
	}
}

func TestInvalidateScan_OnlyAffectsTargetDrive(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	// Scan drive 0 (returns DEADPOOL_2 via mockDriveExecutor).
	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("ScanDisc drive 0: %v", err)
	}

	// Inject a second scan for drive 1.
	orch.InjectCachedScan(1, &makemkv.DiscScan{
		DiscName:   "OTHER_DISC",
		DriveIndex: 1,
	})

	// Invalidate only drive 0.
	orch.InvalidateScan(0)

	if cached := orch.GetCachedScanByDrive(0); cached != nil {
		t.Errorf("expected drive 0 cache to be nil after invalidation, got %+v", cached)
	}

	cached := orch.GetCachedScanByDrive(1)
	if cached == nil {
		t.Fatal("expected drive 1 cache to still be present")
	}
	if cached.DiscName != "OTHER_DISC" {
		t.Errorf("expected DiscName 'OTHER_DISC', got %q", cached.DiscName)
	}
}

func TestScanDisc_NilScanner(t *testing.T) {
	orch, _, _ := setupOrchestrator(t)

	_, err := orch.ScanDisc(context.Background(), 0)
	if err == nil {
		t.Fatal("expected error when scanner is nil")
	}
	if !strings.Contains(err.Error(), "no scanner configured") {
		t.Errorf("expected error to contain 'no scanner configured', got %q", err.Error())
	}
}
