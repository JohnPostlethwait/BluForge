package workflow

import (
	"errors"
	"os"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// A copy being read by a running rip is not spare space to reclaim. Discarding
// it deletes the directory makemkvcon has open, which fails the rip and
// destroys the very copy the retry would need — and the button offering to do
// it is on the drive page throughout the rip.
//
// The refcount that makes this answerable already exists: retainRecovered
// claims the copy for each job and releaseRecovered drops it. Discard simply
// never consulted it.
func TestDiscardIsRefusedWhileAJobIsRippingFromTheCopy(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := backupFixture(t, "in-use")

	orch.registerRecovered(0, &RecoveredDisc{Dir: dir, Source: makemkv.FileSource(dir)})
	claim := orch.retainRecovered(0)
	if claim == nil {
		t.Fatal("no claim taken; the fixture is wrong")
	}

	err := orch.DiscardBackup(0)
	if err == nil {
		t.Error("DiscardBackup succeeded while a rip was reading the copy")
	}
	if !errors.Is(err, ErrBackupInUse) {
		t.Errorf("error = %v, want ErrBackupInUse so the page can say why", err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("the copy was deleted out from under a running rip: %v", statErr)
	}
}

// Same guard, reached by disc name from the activity page.
func TestDiscardByDiscIsRefusedWhileAJobIsRippingFromTheCopy(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := backupFixture(t, "in-use-by-disc")

	orch.registerRecovered(0, &RecoveredDisc{
		Dir:       dir,
		Source:    makemkv.FileSource(dir),
		DiscLabel: "SOME_DISC",
	})
	if claim := orch.retainRecovered(0); claim == nil {
		t.Fatal("no claim taken; the fixture is wrong")
	}

	err := orch.DiscardBackupForDisc("SOME_DISC")
	if !errors.Is(err, ErrBackupInUse) {
		t.Errorf("error = %v, want ErrBackupInUse", err)
	}
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Errorf("the copy was deleted out from under a running rip: %v", statErr)
	}
}

// The guard must not make the copy undeletable. Once the last job releases it,
// discarding is exactly what the user asked for.
func TestDiscardSucceedsOnceTheRipHasReleasedTheCopy(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := backupFixture(t, "released")

	orch.registerRecovered(0, &RecoveredDisc{Dir: dir, Source: makemkv.FileSource(dir)})
	claim := orch.retainRecovered(0)
	// A failed rip: the copy is kept rather than discarded, which is the state
	// the manual discard exists for.
	orch.releaseRecovered(claim, false)

	if err := orch.DiscardBackup(0); err != nil {
		t.Fatalf("DiscardBackup after release: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Error("the copy survived a discard that should have removed it")
	}
}

// A copy nothing ever claimed is discardable immediately.
func TestDiscardSucceedsOnAnUnclaimedCopy(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{Store: storeForTest(t)})
	dir := backupFixture(t, "unclaimed")

	orch.registerRecovered(0, &RecoveredDisc{Dir: dir, Source: makemkv.FileSource(dir)})

	if err := orch.DiscardBackup(0); err != nil {
		t.Fatalf("DiscardBackup: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Error("the copy survived a discard that should have removed it")
	}
}
