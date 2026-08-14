package workflow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

// The first real salvage was deleted by the startup sweep. It records its
// scratch only on success, so a copy that existed for two hours and held 11.8GB
// of rescued data was indistinguishable from crash debris when the container
// restarted — the same mechanism that destroyed a 97GB backup earlier in this
// project.
func TestAnUnfinishedSalvageSurvivesTheSweep(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.outputDir = outputDir

	dir := filepath.Join(outputDir, ScratchDirName, "RAMBO_DISC2-abcd1234")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 0, DiscLabel: "RAMBO_DISC2", BackupDir: dir, Partial: true,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}

	// A restart: reload what was recorded, then sweep.
	if err := orch.RestoreBackups(); err != nil {
		t.Fatalf("RestoreBackups: %v", err)
	}
	tracked, err := orch.TrackedBackupDirs()
	if err != nil {
		t.Fatalf("TrackedBackupDirs: %v", err)
	}
	if err := SweepScratch(outputDir, tracked); err != nil {
		t.Fatalf("SweepScratch: %v", err)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the sweep deleted an unfinished salvage: %v", err)
	}
}

// It is protected, but it is not a disc that can be ripped: half a film with no
// scan behind it must not be offered as a source.
func TestAnUnfinishedSalvageIsNotOfferedAsRippable(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.outputDir = outputDir

	dir := filepath.Join(outputDir, ScratchDirName, "RAMBO_DISC2-abcd1234")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 0, DiscLabel: "RAMBO_DISC2", BackupDir: dir, Partial: true,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}

	if err := orch.RestoreBackups(); err != nil {
		t.Fatalf("RestoreBackups: %v", err)
	}
	if src := orch.RecoveredSource(0); src != nil {
		t.Errorf("an unfinished salvage was offered as a rip source: %v", src.Arg())
	}
}

// A finished recovery still restores as rippable, as it always did.
func TestAFinishedBackupIsStillRestored(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.outputDir = outputDir

	dir := filepath.Join(outputDir, ScratchDirName, "DEADPOOL-abcd1234")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 0, DiscLabel: "DEADPOOL", BackupDir: dir,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}

	if err := orch.RestoreBackups(); err != nil {
		t.Fatalf("RestoreBackups: %v", err)
	}
	if orch.RecoveredSource(0) == nil {
		t.Error("a finished backup was not restored as a rip source")
	}
}
