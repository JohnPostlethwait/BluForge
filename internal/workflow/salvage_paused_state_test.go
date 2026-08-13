package workflow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

// The paused state lived only in the browser. Reloading the page after a pause
// showed nothing at all: no panel, no resume, and a card offering to start over
// on top of hours of recovered data. It has to come from what is recorded.
func TestAPausedSalvageIsReportedAfterAReload(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.outputDir = outputDir

	dir := filepath.Join(outputDir, ScratchDirName, "RAMBO_DISC2-abcd1234")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "00000.m2ts.map"), []byte("# map"), 0o644); err != nil {
		t.Fatalf("write map: %v", err)
	}
	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 1, DiscLabel: "RAMBO_DISC2", BackupDir: dir, Partial: true,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}

	got := orch.CurrentSalvage()
	if !got.Paused {
		t.Fatal("a stopped salvage with work on disk was not reported as paused")
	}
	if got.Active {
		t.Error("a paused salvage was reported as running")
	}
	if !got.Resumable {
		t.Error("a paused salvage was not offered as resumable")
	}
	if got.DiscLabel != "RAMBO_DISC2" {
		t.Errorf("DiscLabel = %q, want RAMBO_DISC2", got.DiscLabel)
	}
	if got.DriveIndex != 1 {
		t.Errorf("DriveIndex = %d, want 1", got.DriveIndex)
	}
}

// A finished salvage is not paused: its copy is recorded as complete, and
// offering to resume it would be nonsense.
func TestAFinishedSalvageIsNotReportedAsPaused(t *testing.T) {
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

	if got := orch.CurrentSalvage(); got.Paused {
		t.Errorf("a completed backup was offered as a paused salvage: %+v", got)
	}
}

// A record whose directory has been removed is not something to resume.
func TestAPausedSalvageWithNoDirectoryIsNotReported(t *testing.T) {
	orch, store, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.outputDir = outputDir

	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 0, DiscLabel: "GONE", BackupDir: filepath.Join(outputDir, "not-here"), Partial: true,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}

	if got := orch.CurrentSalvage(); got.Paused {
		t.Errorf("a record with no directory was offered as resumable: %+v", got)
	}
}

// Nothing on disk, nothing to say.
func TestNoSalvageIsReportedWhenThereIsNone(t *testing.T) {
	orch, _, outputDir := setupOrchestratorWithScanner(t, &mockDriveExecutor{})
	orch.outputDir = outputDir

	got := orch.CurrentSalvage()
	if got.Active || got.Paused {
		t.Errorf("reported a salvage with none anywhere: %+v", got)
	}
}
