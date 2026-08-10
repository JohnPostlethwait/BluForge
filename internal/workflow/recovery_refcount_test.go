package workflow

import (
	"os"
	"path/filepath"
	"testing"
)

// mkBackupDir creates a stand-in backup directory.
func mkBackupDir(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return dir
}

// Swapping discs in one drive while a rip is still running against the previous
// backup must not cross the two: the old job's release has to retire the old
// backup, not decrement the new one. Getting this wrong deletes a backup that is
// actively being ripped from.
func TestReleaseAppliesToTheBackupTheJobRetained(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{})

	firstDir := mkBackupDir(t, "first")
	secondDir := mkBackupDir(t, "second")

	orch.registerRecovered(0, &RecoveredDisc{Dir: firstDir})
	claim := orch.retainRecovered(0)
	if claim == nil {
		t.Fatal("retainRecovered returned no claim for a registered disc")
	}

	// A different disc goes into the same drive and is recovered.
	orch.registerRecovered(0, &RecoveredDisc{Dir: secondDir})

	// The job that was ripping from the first backup finishes.
	orch.releaseRecovered(claim)

	if _, err := os.Stat(firstDir); !os.IsNotExist(err) {
		t.Error("the first backup was not removed when its last job finished")
	}
	if _, err := os.Stat(secondDir); err != nil {
		t.Errorf("the second backup was removed by an unrelated job's release: %v", err)
	}
	if orch.RecoveredDir(0) != secondDir {
		t.Errorf("RecoveredDir(0) = %q, want the second backup %q", orch.RecoveredDir(0), secondDir)
	}
}

// A backup is only removed once every job holding it has finished.
func TestBackupSurvivesUntilLastJobReleases(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{})
	dir := mkBackupDir(t, "shared")

	orch.registerRecovered(1, &RecoveredDisc{Dir: dir})
	first := orch.retainRecovered(1)
	second := orch.retainRecovered(1)

	orch.releaseRecovered(first)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("backup removed while a job still held it: %v", err)
	}

	orch.releaseRecovered(second)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("backup was not removed after the last job released it")
	}
}

// Replacing a disc whose backup nothing is using should clean up immediately.
func TestUnusedBackupRemovedOnReplacement(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{})
	oldDir := mkBackupDir(t, "old")
	newDir := mkBackupDir(t, "new")

	orch.registerRecovered(2, &RecoveredDisc{Dir: oldDir})
	orch.registerRecovered(2, &RecoveredDisc{Dir: newDir})

	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("an unused backup was left behind when its drive got a new disc")
	}
}

// Ejecting a disc while a rip is still reading from its backup must not delete
// the backup out from under the rip.
func TestEjectDoesNotRemoveBackupInUse(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{})
	dir := mkBackupDir(t, "inuse")

	orch.registerRecovered(3, &RecoveredDisc{Dir: dir})
	claim := orch.retainRecovered(3)

	orch.ReleaseRecoveredForDrive(3)

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("eject removed a backup that a rip was still using: %v", err)
	}

	orch.releaseRecovered(claim)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("backup was not removed once the rip finished")
	}
}
