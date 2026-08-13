package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

// The scratch directory name hashes the device path, and optical devices
// renumber: Rambo moved from sr1 to sr2 mid-session. Computing a fresh slug on
// resume would strand an hour of recovered data in a directory nothing looks at
// and start the disc over.
func TestSalvageFindsAnExistingScratchAfterTheDriveRenumbers(t *testing.T) {
	outputDir := t.TempDir()
	// A scratch written when the disc was on one device path.
	old := filepath.Join(outputDir, ScratchDirName, scratchSlug("RAMBO_DISC2", "/dev/sr1"))
	if err := os.MkdirAll(old, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	found, ok := FindSalvageScratch(outputDir, "RAMBO_DISC2")
	if !ok {
		t.Fatal("the existing copy was not found")
	}
	if found != old {
		t.Errorf("found %q, want the copy made under the old device path %q", found, old)
	}
}

func TestSalvageFindsNothingForADiscWithNoScratch(t *testing.T) {
	if _, ok := FindSalvageScratch(t.TempDir(), "NEVER_SALVAGED"); ok {
		t.Error("a disc with no scratch was reported as resumable")
	}
}

// An empty label would match the first directory it found, which could be any
// other disc's copy.
func TestSalvageFindsNothingWithoutADiscLabel(t *testing.T) {
	outputDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outputDir, ScratchDirName, "SOMETHING"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, ok := FindSalvageScratch(outputDir, ""); ok {
		t.Error("an empty disc label matched another disc's copy")
	}
}

// Resuming means continuing a rescue, which is what the map file records. A
// directory holding only a backup has nothing to resume from and should be
// offered as a fresh salvage.
func TestResumableRequiresARescueMap(t *testing.T) {
	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.outputDir = outputDir

	dir := filepath.Join(outputDir, ScratchDirName, scratchSlug("RAMBO_DISC2", "/dev/sr2"))
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if orch.SalvageResumable("RAMBO_DISC2") {
		t.Error("a copy with no rescue map was reported as resumable")
	}

	if err := os.WriteFile(filepath.Join(dir, "00000.m2ts.map"), []byte("# map"), 0o644); err != nil {
		t.Fatalf("write map: %v", err)
	}
	if !orch.SalvageResumable("RAMBO_DISC2") {
		t.Error("a copy with a rescue map was not reported as resumable")
	}
}

// Pausing a salvage that is not running is not an error, just nothing to do.
func TestCancelSalvageOnAnIdleDriveReportsNothingToStop(t *testing.T) {
	orch, _, _ := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	if orch.CancelSalvage(0) {
		t.Error("cancelling an idle drive claimed to have stopped something")
	}
}
