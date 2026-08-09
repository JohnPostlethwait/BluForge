package workflow

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// failingScanner reproduces what a spurious-AACS disc does: MakeMKV reports a
// volume-key failure, fails to open the disc, and returns no titles.
type failingScanner struct {
	devicePath string
}

func (f *failingScanner) ScanDisc(_ context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	return nil, &makemkv.ScanError{
		Source: makemkv.DiscSource(driveIndex),
		Reason: "Failed to open disc",
		Scan: &makemkv.DiscScan{
			DriveIndex: driveIndex,
			DiscName:   "SPURIOUS_DISC",
			Messages: []makemkv.Message{
				{Code: makemkv.MsgVolumeKeyUnknown, Text: "The volume key is unknown for this disc"},
				{Code: makemkv.MsgFailedToOpenDisc, Text: "Failed to open disc"},
			},
		},
	}
}

func (f *failingScanner) DevicePathForDrive(_ context.Context, _ int) string {
	return f.devicePath
}

// selectionRecordingExecutor captures the rip source and track selection so the
// test can prove recovery did not quietly drop them.
type selectionRecordingExecutor struct {
	mu         sync.Mutex
	sources    []makemkv.Source
	selections []*makemkv.SelectionOpts
	started    chan struct{}
}

func (s *selectionRecordingExecutor) StartRip(_ context.Context, src makemkv.Source, _ int, outputDir string, onEvent func(makemkv.Event), sel *makemkv.SelectionOpts) error {
	s.mu.Lock()
	s.sources = append(s.sources, src)
	s.selections = append(s.selections, sel)
	s.mu.Unlock()

	_ = os.WriteFile(filepath.Join(outputDir, "title_t00.mkv"), []byte("fake"), 0o644)
	if onEvent != nil {
		onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Total: 100, Max: 100}})
	}
	select {
	case s.started <- struct{}{}:
	default:
	}
	return nil
}

func (s *selectionRecordingExecutor) snapshot() ([]makemkv.Source, []*makemkv.SelectionOpts) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]makemkv.Source(nil), s.sources...), append([]*makemkv.SelectionOpts(nil), s.selections...)
}

func setupPipeline(t *testing.T) (*Orchestrator, *selectionRecordingExecutor, string) {
	t.Helper()

	tmp := t.TempDir()
	store, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	output := filepath.Join(tmp, "output")
	if err := os.MkdirAll(output, 0o777); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}

	discRoot := writeFakeDisc(t, false)
	exec := &selectionRecordingExecutor{started: make(chan struct{}, 4)}

	orch := NewOrchestrator(OrchestratorDeps{
		Store:       store,
		Engine:      ripper.NewEngine(exec),
		Organizer:   organizer.New(),
		OnBroadcast: func(string, string) {},
		Scanner:     &failingScanner{devicePath: "/dev/sr0"},
		Backupper:   &fakeBackupper{discRoot: discRoot},
		OpenDiscRoot: func(string) (string, func(), error) {
			return discRoot, func() {}, nil
		},
	})

	return orch, exec, output
}

// A disc that trips the signature and proves unencrypted is recovered
// transparently: ScanDisc returns titles where it previously returned an error.
func TestScanDiscRecoversSpuriousAACSDisc(t *testing.T) {
	orch, _, output := setupPipeline(t)
	orch.SetOutputDir(output)

	scan, err := orch.ScanDisc(context.Background(), 0)
	if err != nil {
		t.Fatalf("ScanDisc: %v", err)
	}
	if len(scan.Titles) == 0 {
		t.Fatal("recovered scan has no titles")
	}
	if got := orch.RecoveredSource(0); got == nil || got.IsDisc() {
		t.Errorf("recovered source = %v, want a folder source registered for drive 0", got)
	}
}

// The requirement most at risk of silent regression: a recovered disc must rip
// with the same track selection a cleanly scanning disc would have used, and
// from the backup folder rather than the drive.
func TestRecoveredDiscRipsFromBackupWithTrackSelection(t *testing.T) {
	orch, exec, output := setupPipeline(t)
	orch.SetOutputDir(output)

	scan, err := orch.ScanDisc(context.Background(), 0)
	if err != nil {
		t.Fatalf("ScanDisc: %v", err)
	}

	sel := &makemkv.SelectionOpts{
		AudioLangs:    []string{"eng", "jpn"},
		SubtitleLangs: []string{"eng"},
		KeepForced:    true,
		KeepLossless:  true,
	}

	result := orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        scan.DiscName,
		OutputDir:       output,
		DuplicateAction: "overwrite",
		MediaTitle:      "Recovered Feature",
		SelectionOpts:   sel,
		Titles: []TitleSelection{
			{TitleIndex: 0, TitleName: "Feature", SizeBytes: 1024},
		},
	})
	if result.HasErrors() {
		t.Fatalf("ManualRip reported errors: %s", result.ErrorSummary())
	}

	select {
	case <-exec.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the rip to start")
	}

	sources, selections := exec.snapshot()
	if len(sources) == 0 {
		t.Fatal("no rip was issued")
	}
	if sources[0].IsDisc() {
		t.Errorf("rip issued against %v, want the stripped backup folder", sources[0])
	}
	if selections[0] == nil {
		t.Fatal("track selection was dropped on the recovered rip")
	}
	if len(selections[0].AudioLangs) != 2 || selections[0].AudioLangs[0] != "eng" {
		t.Errorf("audio selection = %v, want [eng jpn]", selections[0].AudioLangs)
	}
	if !selections[0].KeepForced {
		t.Error("KeepForced was dropped on the recovered rip")
	}
}

// Once every job for a recovered disc has finished, its ~100GB backup goes away.
func TestRecoveredBackupCleanedUpAfterRip(t *testing.T) {
	orch, exec, output := setupPipeline(t)
	orch.SetOutputDir(output)

	if _, err := orch.ScanDisc(context.Background(), 0); err != nil {
		t.Fatalf("ScanDisc: %v", err)
	}
	backupDir := orch.RecoveredDir(0)
	if backupDir == "" {
		t.Fatal("no backup directory registered")
	}

	result := orch.ManualRip(ManualRipParams{
		DriveIndex:      0,
		DiscName:        "SPURIOUS_DISC",
		OutputDir:       output,
		DuplicateAction: "overwrite",
		MediaTitle:      "Recovered Feature",
		Titles: []TitleSelection{
			{TitleIndex: 0, TitleName: "Feature", SizeBytes: 1024},
		},
	})
	if result.HasErrors() {
		t.Fatalf("ManualRip reported errors: %s", result.ErrorSummary())
	}

	select {
	case <-exec.started:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the rip to start")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(backupDir); os.IsNotExist(err) {
			return // cleaned up
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("backup %s was not cleaned up after the rip finished", backupDir)
}
