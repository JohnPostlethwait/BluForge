package workflow

import (
	"path/filepath"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// The label has to be the one the rest of BluForge uses for this disc — the
// drive shows it, the jobs record it, and a history entry offering to delete
// the copy matches on it. A folder scan of the copy reports whatever the copied
// BDMV calls itself, which is a different string or no string at all.
func TestACopyIsLabelledFromTheDiscNotTheRescan(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := backupFixture(t, "RAMBO_DISC2-7a434719")

	orch.registerRecovered(1, &RecoveredDisc{
		DiscLabel: "RAMBO_DISC2",
		Dir:       dir,
		Source:    makemkv.FileSource(dir),
		// What MakeMKV made of the copied folder, which is not the disc.
		Scan: &makemkv.DiscScan{DiscName: "BDMV"},
	})

	discs := orch.DiscsWithBackup()
	if len(discs) != 1 || discs[0] != "RAMBO_DISC2" {
		t.Fatalf("DiscsWithBackup() = %v, want [RAMBO_DISC2]", discs)
	}
}

// Copies made before the label was recorded still have to be actionable: the
// directory scratchSlug named after the disc is the only record left.
func TestALabellessCopyIsNamedByItsDirectory(t *testing.T) {
	if got := discLabelFromDir("/output/.bluforge-scratch/RAMBO_DISC2-7a434719"); got != "RAMBO_DISC2" {
		t.Errorf("discLabelFromDir = %q, want RAMBO_DISC2", got)
	}
	// A symlink tree carries the same name with a suffix.
	if got := discLabelFromDir("/output/.bluforge-scratch/DEADPOOL_2-0badf00d-link"); got != "DEADPOOL_2" {
		t.Errorf("discLabelFromDir = %q, want DEADPOOL_2", got)
	}
	// Disc names contain hyphens; only the 8-hex slug suffix may be cut.
	if got := discLabelFromDir("/output/.bluforge-scratch/BLADE-RUNNER-2049-deadbeef"); got != "BLADE-RUNNER-2049" {
		t.Errorf("discLabelFromDir = %q, want the full hyphenated name", got)
	}
}

// Guessing a name from a directory that does not encode one would offer to
// delete a copy under a disc name that was never on the disc.
func TestADirectoryWithNoLabelYieldsNone(t *testing.T) {
	for _, dir := range []string{
		"/output/.bluforge-scratch/disc-7a434719", // scratchSlug's placeholder
		"/output/.bluforge-scratch/orphan",        // no slug at all
		"/output/.bluforge-scratch/-7a434719",     // empty label
		"/output/.bluforge-scratch/thing-notahex", // not a slug suffix
	} {
		if got := discLabelFromDir(dir); got != "" {
			t.Errorf("discLabelFromDir(%q) = %q, want no label", dir, got)
		}
	}
}

// An earlier version recorded whatever scanning the copied folder reported, so
// the stored label can be present and simply wrong. The directory still carries
// the disc's own name, and matching on either is what makes those copies
// actionable without a migration.
func TestACopyRecordedUnderTheWrongNameIsStillMatchable(t *testing.T) {
	store := storeForTest(t)
	orch := NewOrchestrator(OrchestratorDeps{Store: store})

	dir := backupFixture(t, "RAMBO_DISC2-7a434719")
	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 1,
		DiscLabel:  "BDMV", // what a folder scan made of the copy
		BackupDir:  dir,
		SourceArg:  "file:" + dir,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}
	if err := orch.RestoreBackups(); err != nil {
		t.Fatalf("RestoreBackups: %v", err)
	}

	found := map[string]bool{}
	for _, d := range orch.DiscsWithBackup() {
		found[d] = true
	}
	if !found["RAMBO_DISC2"] {
		t.Fatalf("the copy is not offered under the disc's own name; offered %v", orch.DiscsWithBackup())
	}

	// And the history row's disc name has to actually delete it.
	if err := orch.DiscardBackupForDisc("RAMBO_DISC2"); err != nil {
		t.Errorf("discarding by the disc's own name failed: %v", err)
	}
}

// A copy restored after a restart must still be matchable, including one
// recorded by a version that never wrote the label down.
func TestARestoredCopyKeepsItsDisc(t *testing.T) {
	store := storeForTest(t)
	orch := NewOrchestrator(OrchestratorDeps{Store: store})

	labelled := backupFixture(t, "RAMBO_DISC2-7a434719")
	legacy := backupFixture(t, "POLICE_STORY_2-0badf00d")

	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 1, DiscLabel: "RAMBO_DISC2",
		BackupDir: labelled, SourceArg: "file:" + labelled,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}
	if _, err := store.SaveDiscBackup(db.DiscBackup{
		DriveIndex: 2, DiscLabel: "", // an older version's record
		BackupDir: legacy, SourceArg: "file:" + legacy,
	}); err != nil {
		t.Fatalf("SaveDiscBackup: %v", err)
	}

	if err := orch.RestoreBackups(); err != nil {
		t.Fatalf("RestoreBackups: %v", err)
	}

	found := map[string]bool{}
	for _, d := range orch.DiscsWithBackup() {
		found[d] = true
	}
	if !found["RAMBO_DISC2"] {
		t.Error("a restored copy lost the disc it was made from")
	}
	if !found["POLICE_STORY_2"] {
		t.Errorf("a copy recorded without a label was not recovered from %s", filepath.Base(legacy))
	}
}
