package workflow

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// A page loaded or reconnected during a salvage has seen no events, and a
// salvage can be quiet for minutes at a time. Without this the panel was blank
// while the drive was working.
func TestCurrentSalvageTellsAFreshPageWhatIsRunning(t *testing.T) {
	root := discFixture(t, 16)
	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	b := &blockingBackupper{release: make(chan struct{}), started: make(chan struct{})}
	orch.backupper = b
	orch.rescuer = &fakeRescuer{size: 16}
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	orch.outputDir = outputDir

	if got := orch.CurrentSalvage(); got.Active {
		t.Errorf("reported a salvage with none running: %+v", got)
	}

	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("StartSalvage: %v", err)
	}
	<-b.started

	got := orch.CurrentSalvage()
	if !got.Active {
		t.Error("a running salvage was not reported to a fresh page")
	}
	if got.DriveIndex != 0 {
		t.Errorf("DriveIndex = %d, want 0", got.DriveIndex)
	}

	close(b.release)
	waitFor(t, "the salvage to finish", func() bool { return !orch.SalvageInProgress(0) })

	if got := orch.CurrentSalvage(); got.Active {
		t.Errorf("a finished salvage is still reported as running: %+v", got)
	}
}

// broadcastSalvage reads state the salvage goroutine writes, and did so without
// the lock. Tests never caught it because the write always happened before the
// reader started; concurrent readers make it a race.
func TestSalvageResumableIsSafeUnderConcurrentAccess(t *testing.T) {
	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.outputDir = outputDir

	dir := filepath.Join(outputDir, ScratchDirName, "RAMBO_DISC2-abcd1234")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "00000.m2ts.map"), []byte("# map"), 0o644); err != nil {
		t.Fatalf("write map: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if n%2 == 0 {
				orch.recoveredMu.Lock()
				if orch.salvageLabels == nil {
					orch.salvageLabels = make(map[int]string)
				}
				orch.salvageLabels[0] = "RAMBO_DISC2"
				orch.recoveredMu.Unlock()
				return
			}
			orch.salvageResumableFor(0)
		}(i)
	}
	wg.Wait()
}

// Two drives can be salvaged at once. Neither may be reported as the other.
func TestSalvageTracksEachDriveSeparately(t *testing.T) {
	root := discFixture(t, 16)
	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	b := &blockingBackupper{release: make(chan struct{}), started: make(chan struct{})}
	orch.backupper = b
	orch.rescuer = &fakeRescuer{size: 16}
	orch.openDiscRoot = func(string) (string, func(), error) { return root, func() {}, nil }
	orch.outputDir = outputDir

	if err := orch.StartSalvage(0); err != nil {
		t.Fatalf("StartSalvage(0): %v", err)
	}
	<-b.started

	// Drive 1 is a different claim and must be allowed.
	if err := orch.StartSalvage(1); err != nil {
		t.Fatalf("StartSalvage(1) was refused: %v", err)
	}
	if !orch.SalvageInProgress(0) || !orch.SalvageInProgress(1) {
		t.Error("both drives should report a salvage in progress")
	}

	// Stopping one must not stop the other.
	orch.CancelSalvage(0)
	if !orch.SalvageInProgress(1) {
		t.Error("cancelling drive 0 stopped drive 1")
	}
	orch.CancelSalvage(1)
	close(b.release)
}

var _ = context.Canceled
var _ = makemkv.SourceFile
