package workflow

import (
	"os"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

func registerCopy(t *testing.T, orch *Orchestrator, driveIndex int, disc string) string {
	t.Helper()
	dir := backupFixture(t, disc)
	orch.registerRecovered(driveIndex, &RecoveredDisc{
		Dir:    dir,
		Source: makemkv.FileSource(dir),
		Scan:   &makemkv.DiscScan{DiscName: disc},
	})
	return dir
}

// Activity history outlives the drive a rip ran on — drives are renumbered when
// the bus re-enumerates — so a copy has to be findable by the disc it came
// from, which is the only identifier a history row can still trust.
func TestACopyCanBeFoundByItsDisc(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	registerCopy(t, orch, 1, "RAMBO_DISC2")

	discs := orch.DiscsWithBackup()
	if len(discs) != 1 || discs[0] != "RAMBO_DISC2" {
		t.Fatalf("DiscsWithBackup() = %v, want [RAMBO_DISC2]", discs)
	}
}

// Discarding by disc must delete the copy on disk, not merely forget it.
func TestDiscardingByDiscRemovesTheCopy(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := registerCopy(t, orch, 1, "RAMBO_DISC2")

	if err := orch.DiscardBackupForDisc("RAMBO_DISC2"); err != nil {
		t.Fatalf("DiscardBackupForDisc: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the copy is still on disk at %s", dir)
	}
	if discs := orch.DiscsWithBackup(); len(discs) != 0 {
		t.Errorf("still offering %v after discarding it", discs)
	}
}

// A history row naming one disc must never reach into another disc's copy.
func TestDiscardingOneDiscLeavesTheOthers(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	rambo := registerCopy(t, orch, 1, "RAMBO_DISC2")
	police := registerCopy(t, orch, 2, "POLICE_STORY_2")

	if err := orch.DiscardBackupForDisc("RAMBO_DISC2"); err != nil {
		t.Fatalf("DiscardBackupForDisc: %v", err)
	}
	if _, err := os.Stat(rambo); !os.IsNotExist(err) {
		t.Error("the named disc's copy survived")
	}
	if _, err := os.Stat(police); err != nil {
		t.Errorf("another disc's copy was deleted: %v", err)
	}
}

// A disc with no copy is a button that does nothing; the caller has to be able
// to tell, rather than silently succeeding.
func TestDiscardingADiscWithNoCopyFails(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})

	if err := orch.DiscardBackupForDisc("NEVER_SALVAGED"); err == nil {
		t.Error("discarding a copy that does not exist reported success")
	}
	if err := orch.DiscardBackupForDisc(""); err == nil {
		t.Error("discarding an unnamed disc reported success")
	}
}

// A link tree is kilobytes of symlinks that vanish with the disc. There is no
// space to reclaim, so offering to free it is a lie about what the button does.
func TestALinkTreeIsNotOfferedForDiscarding(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := backupFixture(t, "LINKED_DISC")
	orch.registerRecovered(3, &RecoveredDisc{
		Dir:       dir,
		Source:    makemkv.FileSource(dir),
		Scan:      &makemkv.DiscScan{DiscName: "LINKED_DISC"},
		Ephemeral: true,
	})

	if discs := orch.DiscsWithBackup(); len(discs) != 0 {
		t.Errorf("offered to discard a link tree: %v", discs)
	}
}
