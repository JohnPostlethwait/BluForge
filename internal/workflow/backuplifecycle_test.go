package workflow

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

func backupFixture(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, "BDMV", "STREAM"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return dir
}

func storeForTest(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// A copy that took tens of minutes and ~100GB is only worth discarding once it
// has actually produced something. A rip that failed leaves it in place so the
// user can retry without re-reading the disc.
func TestFailedRipKeepsTheBackup(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := backupFixture(t, "kept")

	orch.registerRecovered(0, &RecoveredDisc{Dir: dir, Source: makemkv.FileSource(dir)})
	claim := orch.retainRecovered(0)
	orch.releaseRecovered(claim, false)

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("backup was deleted after a failed rip: %v", err)
	}
}

func TestSuccessfulRipDeletesTheBackup(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := backupFixture(t, "discarded")

	orch.registerRecovered(0, &RecoveredDisc{Dir: dir, Source: makemkv.FileSource(dir)})
	claim := orch.retainRecovered(0)
	orch.releaseRecovered(claim, true)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("backup survived a successful rip")
	}
}

// One title failing is enough to keep the copy: the user will want to retry it.
func TestOneFailedTitleAmongSeveralKeepsTheBackup(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := backupFixture(t, "mixed")

	orch.registerRecovered(0, &RecoveredDisc{Dir: dir, Source: makemkv.FileSource(dir)})
	first := orch.retainRecovered(0)
	second := orch.retainRecovered(0)

	orch.releaseRecovered(first, true)
	orch.releaseRecovered(second, false)

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("backup was deleted although one title failed: %v", err)
	}
}

// Ejecting the disc no longer throws the copy away — the user asked for it to
// persist until either a successful rip or an explicit discard.
func TestEjectKeepsTheBackup(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := backupFixture(t, "ejected")

	orch.registerRecovered(1, &RecoveredDisc{Dir: dir, Source: makemkv.FileSource(dir)})
	orch.ReleaseRecoveredForDrive(1)

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("eject deleted the backup: %v", err)
	}
}

// The manual escape hatch: reclaim the space on demand.
func TestDiscardBackupRemovesItAndItsRecord(t *testing.T) {
	store := storeForTest(t)
	orch := NewOrchestrator(OrchestratorDeps{Store: store})
	dir := backupFixture(t, "manual")

	orch.registerRecovered(2, &RecoveredDisc{Dir: dir, Source: makemkv.FileSource(dir)})

	if err := orch.DiscardBackup(2); err != nil {
		t.Fatalf("DiscardBackup: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("discard left the backup on disk")
	}
	if got := orch.RecoveredDir(2); got != "" {
		t.Errorf("drive still reports a backup at %q", got)
	}
	rows, err := store.ListDiscBackups()
	if err != nil {
		t.Fatalf("ListDiscBackups: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("record survived the discard: %+v", rows)
	}
}

// A backup outlives a restart, or the copy is orphaned and the next scan goes
// back to a drive that cannot be read.
func TestBackupsRestoreAfterRestart(t *testing.T) {
	store := storeForTest(t)
	dir := backupFixture(t, "persisted")

	first := NewOrchestrator(OrchestratorDeps{Store: store})
	first.registerRecovered(1, &RecoveredDisc{Dir: dir, Source: makemkv.FileSource(dir)})

	// A new process comes up against the same database.
	second := NewOrchestrator(OrchestratorDeps{Store: store})
	if err := second.RestoreBackups(); err != nil {
		t.Fatalf("RestoreBackups: %v", err)
	}

	if got := second.RecoveredDir(1); got != dir {
		t.Errorf("RecoveredDir = %q, want %q", got, dir)
	}
	if src := second.RecoveredSource(1); src == nil || src.IsDisc() {
		t.Errorf("recovered source = %v, want the folder source", src)
	}
}

// A record whose directory is gone — deleted by hand, or a volume that did not
// mount — must not leave the drive pointing at nothing.
func TestRestoreDropsRecordsWithNoDirectory(t *testing.T) {
	store := storeForTest(t)
	missing := filepath.Join(t.TempDir(), "gone")

	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 0, BackupDir: missing, SourceArg: "file:" + missing,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}

	orch := NewOrchestrator(OrchestratorDeps{Store: store})
	if err := orch.RestoreBackups(); err != nil {
		t.Fatalf("RestoreBackups: %v", err)
	}

	if got := orch.RecoveredDir(0); got != "" {
		t.Errorf("restored a backup whose directory does not exist: %q", got)
	}
	rows, _ := store.ListDiscBackups()
	if len(rows) != 0 {
		t.Errorf("stale record was not cleaned up: %+v", rows)
	}
}

// The startup sweep must remove crash debris without touching a tracked copy —
// deleting a live 100GB backup was the previous behaviour.
func TestSweepSpareseOnlyUntrackedDirectories(t *testing.T) {
	output := t.TempDir()
	scratch := filepath.Join(output, ScratchDirName)
	tracked := filepath.Join(scratch, "tracked")
	orphan := filepath.Join(scratch, "orphan")
	for _, d := range []string{tracked, orphan} {
		if err := os.MkdirAll(d, 0o777); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	if err := SweepScratch(output, []string{tracked}); err != nil {
		t.Fatalf("SweepScratch: %v", err)
	}

	if _, err := os.Stat(tracked); err != nil {
		t.Errorf("sweep deleted a tracked backup: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("sweep left untracked debris behind")
	}
}
