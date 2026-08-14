package workflow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// scratchCopy makes a copy under the output dir's scratch root, where the sweep
// will actually see it.
func scratchCopy(t *testing.T, outputDir, name string) string {
	t.Helper()
	dir := filepath.Join(outputDir, ScratchDirName, name)
	if err := os.MkdirAll(filepath.Join(dir, "BDMV", "STREAM"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return dir
}

// Copies are held in a map keyed by drive index, so a second copy made on the
// same drive displaces the first. The first is still on disk and still recorded,
// but it stopped being named as live — and the sweep deletes everything it is
// not told to keep. That is hours of work gone on the next restart, silently.
func TestASecondCopyOnTheSameDriveDoesNotGetTheFirstOneSwept(t *testing.T) {
	outputDir := t.TempDir()
	store := storeForTest(t)
	orch := NewOrchestrator(OrchestratorDeps{Store: store})

	first := scratchCopy(t, outputDir, "RAMBO_DISC2-7a434719")
	second := scratchCopy(t, outputDir, "INVICTUS-0badf00d")

	orch.registerRecovered(1, &RecoveredDisc{
		DiscLabel: "RAMBO_DISC2", Dir: first, Source: makemkv.FileSource(first),
	})
	// The same drive, a second disc: this displaces the first in the map.
	orch.registerRecovered(1, &RecoveredDisc{
		DiscLabel: "INVICTUS", Dir: second, Source: makemkv.FileSource(second),
	})

	tracked, err := orch.TrackedBackupDirs()
	if err != nil {
		t.Fatalf("TrackedBackupDirs: %v", err)
	}
	if err := SweepScratch(outputDir, tracked); err != nil {
		t.Fatalf("SweepScratch: %v", err)
	}

	if _, err := os.Stat(first); err != nil {
		t.Errorf("the sweep deleted the displaced copy: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Errorf("the sweep deleted the current copy: %v", err)
	}
}

// A copy that was discarded is no longer recorded, and must not be resurrected
// as something to keep — the sweep is what reclaims the space.
func TestADiscardedCopyIsNotProtected(t *testing.T) {
	outputDir := t.TempDir()
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})

	dir := scratchCopy(t, outputDir, "RAMBO_DISC2-7a434719")
	orch.registerRecovered(1, &RecoveredDisc{
		DiscLabel: "RAMBO_DISC2", Dir: dir, Source: makemkv.FileSource(dir),
	})
	if err := orch.DiscardBackupForDisc("RAMBO_DISC2"); err != nil {
		t.Fatalf("DiscardBackupForDisc: %v", err)
	}

	tracked, err := orch.TrackedBackupDirs()
	if err != nil {
		t.Fatalf("TrackedBackupDirs: %v", err)
	}
	for _, d := range tracked {
		if d == dir {
			t.Error("a discarded copy is still listed as live")
		}
	}
}

// The sweep deletes everything it is not told to keep, so an incomplete list is
// not a smaller list — it is an instruction to delete. The caller has to be able
// to tell the difference and skip the sweep.
func TestAnUnreadableRecordIsAnErrorRatherThanAnEmptyList(t *testing.T) {
	store := storeForTest(t)
	orch := NewOrchestrator(OrchestratorDeps{Store: store})

	dir := t.TempDir()
	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 1, DiscLabel: "RAMBO_DISC2", BackupDir: dir,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}
	// Losing the database is what makes the list incomplete.
	store.Close()

	tracked, err := orch.TrackedBackupDirs()
	if err == nil {
		t.Fatalf("an unreadable record reported success with %d dirs; the sweep would delete every copy", len(tracked))
	}
	if tracked != nil {
		t.Error("an incomplete list was returned alongside the error")
	}
}
