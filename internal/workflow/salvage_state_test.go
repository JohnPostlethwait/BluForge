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

// Two drives can be salvaged at once, and the claims must be independent. This
// exercises the claim directly rather than running two full salvages: the point
// is the bookkeeping, and two concurrent salvages sharing test fakes proved
// only that the fakes were not thread-safe.
func TestSalvageClaimsAreIndependentPerDrive(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})

	var firstCancelled, secondCancelled bool
	if !orch.beginSalvage(0, func() { firstCancelled = true }) {
		t.Fatal("could not claim drive 0")
	}
	if !orch.beginSalvage(1, func() { secondCancelled = true }) {
		t.Fatal("claiming drive 1 was refused while drive 0 was busy")
	}
	if orch.beginSalvage(0, func() {}) {
		t.Error("drive 0 was claimed twice")
	}

	if !orch.CancelSalvage(0) {
		t.Error("cancelling drive 0 found nothing")
	}
	if !firstCancelled {
		t.Error("drive 0's cancel was not called")
	}
	if secondCancelled {
		t.Error("cancelling drive 0 also cancelled drive 1")
	}
	if !orch.SalvageInProgress(1) {
		t.Error("drive 1 stopped when drive 0 was cancelled")
	}

	orch.endSalvage(0)
	orch.endSalvage(1)
	if orch.SalvageInProgress(0) || orch.SalvageInProgress(1) {
		t.Error("a released claim still reports a salvage in progress")
	}
}

var _ = context.Canceled
var _ = makemkv.SourceFile
