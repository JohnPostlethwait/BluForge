package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// After a restart the scan cache is empty, but the backup on disk is not. A
// restored backup has to be scanned from the folder — falling through to the
// drive means failing the signature again and starting a second ~100GB copy of
// a disc already sitting in scratch.
func TestScanUsesRestoredBackupInsteadOfRebackingUp(t *testing.T) {
	tmp := t.TempDir()
	store, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	output := filepath.Join(tmp, "output")
	if err := os.MkdirAll(output, 0o777); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A completed backup from a previous run, recorded in the database.
	backupDir := filepath.Join(output, ScratchDirName, "disc-abc123")
	if err := os.MkdirAll(filepath.Join(backupDir, "BDMV", "STREAM"), 0o777); err != nil {
		t.Fatalf("mkdir backup: %v", err)
	}
	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 1,
		DiscLabel:  "STRANGER_THINGS",
		BackupDir:  backupDir,
		SourceArg:  "file:" + backupDir,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}

	discRoot := writeFakeDisc(t, false)
	backupper := &fakeBackupper{discRoot: discRoot}

	orch := NewOrchestrator(OrchestratorDeps{
		Store:       store,
		Engine:      ripper.NewEngine(&mockRipExecutor{}),
		Organizer:   organizer.New(),
		OnBroadcast: func(string, string) {},
		Scanner:     &failingScanner{devicePath: "/dev/sr1"},
		Backupper:   backupper,
		OpenDiscRoot: func(string) (string, func(), error) {
			return discRoot, func() {}, nil
		},
	})
	orch.SetOutputDir(output)

	if err := orch.RestoreBackups(); err != nil {
		t.Fatalf("RestoreBackups: %v", err)
	}

	scan, err := orch.ScanDisc(context.Background(), 1)
	if err != nil {
		t.Fatalf("ScanDisc on a restored backup: %v", err)
	}
	if scan == nil || len(scan.Titles) == 0 {
		t.Fatal("restored backup produced no titles")
	}
	if n := backupper.calls(); n != 0 {
		t.Errorf("ran %d backups despite one already being on disk, want 0", n)
	}

	// And it must have read the folder, not the drive.
	backupper.mu.Lock()
	defer backupper.mu.Unlock()
	if len(backupper.scanCalls) == 0 {
		t.Fatal("no scan was issued")
	}
	last := backupper.scanCalls[len(backupper.scanCalls)-1]
	if last.IsDisc() {
		t.Errorf("scanned %v, want the restored backup folder", last)
	}
	if last.Arg() != makemkv.FileSource(backupDir).Arg() {
		t.Errorf("scanned %q, want %q", last.Arg(), makemkv.FileSource(backupDir).Arg())
	}
}
