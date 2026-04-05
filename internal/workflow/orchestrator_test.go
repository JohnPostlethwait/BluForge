package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func (m *mockRipExecutor) StartRip(_ context.Context, _ int, _ int, outputDir string, onEvent func(makemkv.Event), _ *makemkv.SelectionOpts) error {
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

func waitForCompletedJob(t *testing.T, store *db.Store) {
	t.Helper()
	waitForCompletedJobs(t, store, 1)
}

func waitForCompletedJobs(t *testing.T, store *db.Store, count int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d completed jobs", count)
		case <-tick.C:
			jobs, err := store.ListJobsByStatus("completed")
			if err != nil {
				t.Fatalf("ListJobsByStatus: %v", err)
			}
			if len(jobs) >= count {
				return
			}
		}
	}
}

func TestAutoRip_NoMatch_CreatesContributionRecord(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, outputDir := setupOrchestratorWithScanner(t, scanner)

	cfg := AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
	}

	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip: %v", err)
	}

	waitForCompletedJob(t, store)

	// Verify a contribution record was created.
	contribs, err := store.ListContributions("")
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}
	if len(contribs) != 1 {
		t.Fatalf("expected 1 contribution, got %d", len(contribs))
	}
	c := contribs[0]
	if c.DiscName != "DEADPOOL_2" {
		t.Errorf("DiscName: want %q, got %q", "DEADPOOL_2", c.DiscName)
	}
	if c.Status != "pending" {
		t.Errorf("Status: want %q, got %q", "pending", c.Status)
	}
	if c.ScanJSON == "" {
		t.Error("ScanJSON should not be empty")
	}
	if c.DiscKey == "" {
		t.Error("DiscKey should not be empty")
	}
}

func TestAutoRip_NoMatch_DuplicateContributionSkipped(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, outputDir := setupOrchestratorWithScanner(t, scanner)

	cfg := AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
	}

	// First AutoRip.
	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip 1: %v", err)
	}
	waitForCompletedJob(t, store)

	// Second AutoRip — same disc, same scanner.
	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip 2: %v", err)
	}
	waitForCompletedJobs(t, store, 2)

	// Should still have exactly 1 contribution (not 2).
	contribs, err := store.ListContributions("")
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}
	if len(contribs) != 1 {
		t.Errorf("expected 1 contribution after 2 autorips, got %d", len(contribs))
	}
}

func TestAutoRip_NoMatch_BroadcastsSSE(t *testing.T) {
	scanner := &mockDriveExecutor{}

	var mu sync.Mutex
	var broadcasts []struct{ event, data string }

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
		Store:     store,
		Engine:    engine,
		Organizer: org,
		OnBroadcast: func(event, data string) {
			mu.Lock()
			broadcasts = append(broadcasts, struct{ event, data string }{event, data})
			mu.Unlock()
		},
		Scanner: scanner,
	})

	outputDir := filepath.Join(tmpDir, "output")
	os.MkdirAll(outputDir, 0o755)

	cfg := AutoRipConfig{
		OutputDir:       outputDir,
		DuplicateAction: "overwrite",
	}

	if err := orch.AutoRip(context.Background(), 0, cfg); err != nil {
		t.Fatalf("AutoRip: %v", err)
	}

	// The contribution_available broadcast happens synchronously in AutoRip,
	// so it is already captured by the time AutoRip returns.
	mu.Lock()
	defer mu.Unlock()

	found := false
	for _, b := range broadcasts {
		if b.event == "contribution_available" {
			found = true
			if !strings.Contains(b.data, "contribution_id") {
				t.Errorf("broadcast data missing contribution_id: %s", b.data)
			}
			if !strings.Contains(b.data, "DEADPOOL_2") {
				t.Errorf("broadcast data missing disc name: %s", b.data)
			}
			break
		}
	}
	if !found {
		t.Error("expected contribution_available SSE broadcast, none found")
	}
}

// makeTitleWithStreams builds a TitleInfo with the given audio and subtitle streams
// for testing buildTrackMetadata.
func makeTitleWithStreams(sourceFile string, streams []makemkv.StreamInfo) makemkv.TitleInfo {
	return makemkv.TitleInfo{
		Index: 0,
		Attributes: map[int]string{
			2:  "Test Title",
			9:  "2:05:08",
			10: "56.7 GB",
			11: "60960036864",
			16: sourceFile,
		},
		Streams: streams,
	}
}

func tronStreams() []makemkv.StreamInfo {
	return []makemkv.StreamInfo{
		// TrueHD English 7.1
		{TitleIndex: 0, StreamIndex: 0, Attributes: map[int]string{1: "A_TRUEHD", 3: "eng", 4: "English", 6: "TrueHD", 14: "7.1"}},
		// AC3 English 5.1
		{TitleIndex: 0, StreamIndex: 1, Attributes: map[int]string{1: "A_AC3", 3: "eng", 4: "English", 6: "AC3", 14: "5.1"}},
		// DTS-HD MA French
		{TitleIndex: 0, StreamIndex: 2, Attributes: map[int]string{1: "A_DTSHD", 3: "fra", 4: "French", 6: "DTS-HD MA", 14: "5.1"}},
		// AC3 French
		{TitleIndex: 0, StreamIndex: 3, Attributes: map[int]string{1: "A_AC3", 3: "fra", 4: "French", 6: "AC3", 14: "5.1"}},
		// English subtitle
		{TitleIndex: 0, StreamIndex: 4, Attributes: map[int]string{1: "S_HDMV/PGS", 3: "eng", 4: "English", 6: "PGS"}},
		// French subtitle
		{TitleIndex: 0, StreamIndex: 5, Attributes: map[int]string{1: "S_HDMV/PGS", 3: "fra", 4: "French", 6: "PGS"}},
	}
}

func TestBuildTrackMetadata_NoFilter(t *testing.T) {
	title := makeTitleWithStreams("00800.mpls", tronStreams())
	meta := buildTrackMetadata(&title, nil)

	if len(meta.AudioTracks) != 4 {
		t.Errorf("expected 4 audio tracks (nil opts = no filter), got %d", len(meta.AudioTracks))
	}
	if len(meta.SubtitleLanguages) != 2 {
		t.Errorf("expected 2 subtitle languages, got %d: %v", len(meta.SubtitleLanguages), meta.SubtitleLanguages)
	}
}

func TestBuildTrackMetadata_AudioLangFilter(t *testing.T) {
	title := makeTitleWithStreams("00800.mpls", tronStreams())
	opts := &makemkv.SelectionOpts{
		AudioLangs:    []string{"eng"},
		SubtitleLangs: []string{"eng"},
		KeepLossless:  true,
	}
	meta := buildTrackMetadata(&title, opts)

	if len(meta.AudioTracks) != 2 {
		t.Errorf("expected 2 English audio tracks, got %d", len(meta.AudioTracks))
		for _, a := range meta.AudioTracks {
			t.Logf("  audio: %s %s %s", a.Codec, a.Channels, a.Language)
		}
	}
	for _, a := range meta.AudioTracks {
		if a.Language != "English" {
			t.Errorf("expected only English audio, got %q", a.Language)
		}
	}

	if len(meta.SubtitleLanguages) != 1 || meta.SubtitleLanguages[0] != "English" {
		t.Errorf("expected [English] subtitle, got %v", meta.SubtitleLanguages)
	}
}

func TestBuildTrackMetadata_LosslessFilter(t *testing.T) {
	title := makeTitleWithStreams("00800.mpls", tronStreams())
	opts := &makemkv.SelectionOpts{
		AudioLangs:   []string{"eng"},
		KeepLossless: false,
	}
	meta := buildTrackMetadata(&title, opts)

	// Should keep AC3 English but not TrueHD English.
	if len(meta.AudioTracks) != 1 {
		t.Errorf("expected 1 audio track (AC3 English, TrueHD filtered), got %d", len(meta.AudioTracks))
		for _, a := range meta.AudioTracks {
			t.Logf("  audio: %s %s %s", a.Codec, a.Channels, a.Language)
		}
	}
	if len(meta.AudioTracks) == 1 && meta.AudioTracks[0].Codec != "AC3" {
		t.Errorf("expected AC3, got %q", meta.AudioTracks[0].Codec)
	}
}

func TestAutoRip_WithMatch_NoContributionCreated(t *testing.T) {
	scanner := &mockDriveExecutor{}
	orch, store, outputDir := setupOrchestratorWithScanner(t, scanner)

	// Save a mapping for this disc so AutoRip finds a match.
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

	waitForCompletedJob(t, store)

	// No contribution should exist.
	contribs, err := store.ListContributions("")
	if err != nil {
		t.Fatalf("ListContributions: %v", err)
	}
	if len(contribs) != 0 {
		t.Errorf("expected 0 contributions when disc is matched, got %d", len(contribs))
	}
}
