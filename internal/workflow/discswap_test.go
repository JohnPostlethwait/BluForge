package workflow

import (
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// A copy is bound to a drive, and the drive outlives the disc. Putting a second
// disc in the same drive left it bound to the first: the page announced that an
// unrelated disc was being read from a repaired copy, and a scan would have
// been served that copy's titles instead of the disc's.
func TestASecondDiscIsNotReadFromTheFirstDiscsCopy(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	registerCopy(t, orch, 1, "RAMBO_DISC2")

	orch.SetDriveDisc(1, "INVICTUS")

	if src := orch.RecoveredSource(1); src != nil {
		t.Errorf("a scan of INVICTUS would be served from %s", src.Arg())
	}
	if dir := orch.RecoveredDir(1); dir != "" {
		t.Errorf("the page still says this drive is reading a repaired copy at %s", dir)
	}
	if claim := orch.retainRecovered(1); claim != nil {
		t.Error("a rip of INVICTUS would read the other disc's copy")
	}
}

// The same disc back in the drive is still its own copy.
func TestTheSameDiscKeepsReadingItsCopy(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := registerCopy(t, orch, 1, "RAMBO_DISC2")

	orch.SetDriveDisc(1, "RAMBO_DISC2")

	if got := orch.RecoveredDir(1); got != dir {
		t.Errorf("RecoveredDir = %q, want the disc's own copy at %q", got, dir)
	}
}

// A drive reporting no disc, or one it has not identified yet, is not evidence
// the copy is wrong — and unbinding on it would drop the copy in the gap right
// after a salvage finishes.
func TestAnUnnamedDiscDoesNotUnbindTheCopy(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := registerCopy(t, orch, 1, "RAMBO_DISC2")

	orch.SetDriveDisc(1, "")

	if got := orch.RecoveredDir(1); got != dir {
		t.Errorf("RecoveredDir = %q, want the copy kept at %q", got, dir)
	}
}

// Unbinding is not deleting. The copy cost hours; it stays on disk and stays
// discardable from the history entry it produced.
func TestAnUnboundCopyIsStillOnDiskAndStillDiscardable(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	registerCopy(t, orch, 1, "RAMBO_DISC2")

	orch.SetDriveDisc(1, "INVICTUS")

	discs := orch.DiscsWithBackup()
	if len(discs) != 1 || discs[0] != "RAMBO_DISC2" {
		t.Fatalf("DiscsWithBackup() = %v, want the copy still offered as RAMBO_DISC2", discs)
	}
	if err := orch.DiscardBackupForDisc("RAMBO_DISC2"); err != nil {
		t.Errorf("the copy could no longer be discarded: %v", err)
	}
}

// Swapping a disc out and back in must not send the drive to read the damaged
// original — repairing it is the whole reason the copy exists.
func TestTheOriginalDiscComingBackIsReadFromItsCopyAgain(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := registerCopy(t, orch, 1, "RAMBO_DISC2")

	orch.SetDriveDisc(1, "INVICTUS")
	if orch.RecoveredDir(1) != "" {
		t.Fatal("the copy was still bound while another disc was in the drive")
	}

	orch.SetDriveDisc(1, "RAMBO_DISC2")

	if got := orch.RecoveredDir(1); got != dir {
		t.Errorf("RecoveredDir = %q, want the copy back at %q", got, dir)
	}
	if claim := orch.retainRecovered(1); claim == nil {
		t.Error("a rip would read the damaged disc rather than its repaired copy")
	}
}

// A drive that never had a copy is unaffected.
func TestSettingTheDiscOnAPlainDriveDoesNothing(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})

	orch.SetDriveDisc(2, "INVICTUS")

	if src := orch.RecoveredSource(2); src != nil {
		t.Errorf("a drive with no copy is being served %s", src.Arg())
	}
}

// The salvage note describes the copy's damage. Attached to the wrong disc it
// would mark a perfectly good rip as salvaged.
func TestASecondDiscDoesNotInheritTheSalvageNote(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := backupFixture(t, "RAMBO_DISC2-7a434719")
	orch.registerRecovered(1, &RecoveredDisc{
		DiscLabel: "RAMBO_DISC2",
		Dir:       dir,
		Source:    makemkv.FileSource(dir),
		Salvaged:  true,
		Measured:  true,
	})

	if orch.salvageNoteForDrive(1) == "" {
		t.Fatal("the salvaged copy has no note to begin with")
	}

	orch.SetDriveDisc(1, "INVICTUS")

	if note := orch.salvageNoteForDrive(1); note != "" {
		t.Errorf("a rip of INVICTUS would be marked: %q", note)
	}
}
