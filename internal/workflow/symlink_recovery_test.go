package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// setupSymlinkRecovery builds an orchestrator whose MakeMKV accepts a symlinked
// disc tree, as the real one does.
func setupSymlinkRecovery(t *testing.T) (*Orchestrator, *fakeBackupper, string, *bool) {
	t.Helper()

	tmp := t.TempDir()
	store, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	output := filepath.Join(tmp, "output")
	if err := os.MkdirAll(output, 0o777); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}

	discRoot := writeFakeDisc(t, false)
	backupper := &fakeBackupper{discRoot: discRoot, acceptSymlinkTree: true}

	unmounted := false
	orch := NewOrchestrator(OrchestratorDeps{
		Store:       store,
		Engine:      ripper.NewEngine(&mockRipExecutor{}),
		Organizer:   organizer.New(),
		OnBroadcast: func(string, string) {},
		Backupper:   backupper,
		OpenDiscRoot: func(string) (string, func(), error) {
			return discRoot, func() { unmounted = true }, nil
		},
	})
	orch.SetOutputDir(output)

	return orch, backupper, output, &unmounted
}

// The disc does not need copying. MakeMKV decides whether to demand a volume key
// from the tree it is pointed at, so a link tree without AACS is enough — which
// turns ~100GB and an hour into kilobytes and a moment.
func TestRecoveryPrefersSymlinksOverCopying(t *testing.T) {
	orch, backupper, output, _ := setupSymlinkRecovery(t)

	rec, err := orch.RecoverSpuriousAACS(context.Background(), RecoveryRequest{
		DriveIndex: 0,
		DevicePath: "/dev/sr0",
		DiscLabel:  "SOME_DISC",
		OutputDir:  output,
	})
	if err != nil {
		t.Fatalf("RecoverSpuriousAACS: %v", err)
	}

	if n := backupper.calls(); n != 0 {
		t.Errorf("copied the disc %d times when a link tree would do", n)
	}
	if !rec.Ephemeral {
		t.Error("a link tree should be marked ephemeral")
	}
	if !strings.HasSuffix(rec.Dir, "-link") {
		t.Errorf("rec.Dir = %q, want the link tree", rec.Dir)
	}
	if _, err := os.Stat(filepath.Join(rec.Dir, "AACS")); !os.IsNotExist(err) {
		t.Error("AACS is present in the link tree")
	}
	if _, err := os.Stat(filepath.Join(rec.Dir, "BDMV", "STREAM")); err != nil {
		t.Errorf("BDMV does not resolve through the tree: %v", err)
	}

	// The label comes from the rescan of the tree, not from the failed disc
	// scan — which is the whole point of backfilling it.
	rows, err := orch.store.ListDiscDiagnosticsByLabel("RECOVERED_DISC", 5)
	if err != nil {
		t.Fatalf("ListDiscDiagnosticsByLabel: %v", err)
	}
	if len(rows) == 0 || rows[0].RipPath != "symlink" {
		t.Errorf("diagnostics did not record the symlink path: %+v", rows)
	}
}

// The links point at a mounted disc, so unmounting before the rips finish would
// break them.
func TestSymlinkRecoveryHoldsTheMountUntilReleased(t *testing.T) {
	orch, _, output, unmounted := setupSymlinkRecovery(t)

	rec, err := orch.RecoverSpuriousAACS(context.Background(), RecoveryRequest{
		DriveIndex: 0,
		DevicePath: "/dev/sr0",
		DiscLabel:  "SOME_DISC",
		OutputDir:  output,
	})
	if err != nil {
		t.Fatalf("RecoverSpuriousAACS: %v", err)
	}
	if *unmounted {
		t.Fatal("the disc was unmounted while its link tree was still in use")
	}

	orch.registerRecovered(0, rec)
	claim := orch.retainRecovered(0)
	if *unmounted {
		t.Fatal("unmounted while a job held the tree")
	}

	orch.releaseRecovered(claim, true)
	if !*unmounted {
		t.Error("the mount was not released after the last job finished")
	}
	if _, err := os.Stat(rec.Dir); !os.IsNotExist(err) {
		t.Error("the link tree was left behind")
	}
}

// A link tree is seconds to rebuild and useless once unmounted, so a failed rip
// does not preserve it the way a real copy is preserved.
func TestFailedRipDiscardsLinkTree(t *testing.T) {
	orch, _, output, unmounted := setupSymlinkRecovery(t)

	rec, err := orch.RecoverSpuriousAACS(context.Background(), RecoveryRequest{
		DriveIndex: 0,
		DevicePath: "/dev/sr0",
		DiscLabel:  "SOME_DISC",
		OutputDir:  output,
	})
	if err != nil {
		t.Fatalf("RecoverSpuriousAACS: %v", err)
	}

	orch.registerRecovered(0, rec)
	claim := orch.retainRecovered(0)
	orch.releaseRecovered(claim, false)

	if _, err := os.Stat(rec.Dir); !os.IsNotExist(err) {
		t.Error("link tree kept after a failed rip; only a real copy is worth keeping")
	}
	if !*unmounted {
		t.Error("mount not released after a failed rip")
	}
}

// Nothing about a link tree survives a restart — the links dangle once the disc
// is unmounted — so it must never be recorded as a restorable backup.
func TestLinkTreeIsNotPersisted(t *testing.T) {
	orch, _, output, _ := setupSymlinkRecovery(t)

	rec, err := orch.RecoverSpuriousAACS(context.Background(), RecoveryRequest{
		DriveIndex: 0,
		DevicePath: "/dev/sr0",
		DiscLabel:  "SOME_DISC",
		OutputDir:  output,
	})
	if err != nil {
		t.Fatalf("RecoverSpuriousAACS: %v", err)
	}
	orch.registerRecovered(0, rec)

	backups, err := orch.store.ListDiscBackups()
	if err != nil {
		t.Fatalf("ListDiscBackups: %v", err)
	}
	if len(backups) != 0 {
		t.Errorf("a link tree was persisted as a restorable backup: %+v", backups)
	}
}

// If MakeMKV will not accept the link tree, the disc still gets recovered the
// expensive way rather than failing.
func TestFallsBackToCopyWhenSymlinksRejected(t *testing.T) {
	orch, backupper, output, _ := setupSymlinkRecovery(t)
	backupper.acceptSymlinkTree = false

	rec, err := orch.RecoverSpuriousAACS(context.Background(), RecoveryRequest{
		DriveIndex: 0,
		DevicePath: "/dev/sr0",
		DiscLabel:  "SOME_DISC",
		OutputDir:  output,
	})
	if err != nil {
		t.Fatalf("RecoverSpuriousAACS: %v", err)
	}
	if backupper.calls() != 1 {
		t.Errorf("backup ran %d times, want 1 after the symlink path was refused", backupper.calls())
	}
	if rec.Ephemeral {
		t.Error("a real copy should not be marked ephemeral")
	}
	if strings.HasSuffix(rec.Dir, "-link") {
		t.Errorf("rec.Dir = %q, want the copied backup", rec.Dir)
	}
	// The abandoned link tree must not be left lying around.
	if _, err := os.Stat(rec.Dir + "-link"); !errors.Is(err, os.ErrNotExist) {
		t.Error("the rejected link tree was left behind")
	}
}
