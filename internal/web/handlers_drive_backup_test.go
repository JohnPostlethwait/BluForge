package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// driveStoreFor renders the drive page's state for a drive holding discName,
// with a repaired copy of copyOf already restored against that drive.
func driveStoreFor(t *testing.T, discName, copyOf string) DriveStoreJSON {
	t.Helper()

	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: discName}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	copyDir := filepath.Join(t.TempDir(), copyOf+"-7a434719")
	if err := os.MkdirAll(filepath.Join(copyDir, "BDMV", "STREAM"), 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 0, DiscLabel: copyOf,
		BackupDir: copyDir, SourceArg: "file:" + copyDir,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}

	srv := newTestServer(t, mgr)
	srv.store = store
	srv.orchestrator = workflow.NewOrchestrator(workflow.OrchestratorDeps{Store: store})

	// A restart restores the copy against the drive it was made on, with no
	// disc event having fired yet.
	if err := srv.orchestrator.RestoreBackups(); err != nil {
		t.Fatalf("RestoreBackups: %v", err)
	}

	drv := mgr.GetDrive(0)
	if drv == nil {
		t.Fatal("no drive 0")
	}
	return srv.buildDriveStore(0, drv)
}

// The copy is bound to a drive index, and a restart restores that binding
// before any disc event has fired. A page loaded in that window announced that
// a completely different disc was being read from a repaired copy — and the
// same answer drives what a scan and a rip would read.
func TestTheDrivePageDoesNotClaimADifferentDiscIsARepairedCopy(t *testing.T) {
	got := driveStoreFor(t, "INVICTUS", "RAMBO_DISC2")

	if got.HasBackup {
		t.Error("the page says INVICTUS is being read from a repaired copy of another disc")
	}
}

// The disc the copy was actually made from must still be served from it.
func TestTheDrivePageSaysWhenTheDiscIsItsOwnRepairedCopy(t *testing.T) {
	got := driveStoreFor(t, "RAMBO_DISC2", "RAMBO_DISC2")

	if !got.HasBackup {
		t.Error("the page does not say this disc is being read from its repaired copy")
	}
}
