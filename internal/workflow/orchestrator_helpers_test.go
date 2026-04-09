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
