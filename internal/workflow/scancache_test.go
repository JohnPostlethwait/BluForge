package workflow

import (
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

func scanOf(disc string, titles int) *makemkv.DiscScan {
	s := &makemkv.DiscScan{DiscName: disc}
	for i := range titles {
		s.Titles = append(s.Titles, makemkv.TitleInfo{Index: i})
	}
	return s
}

// The cache is keyed by drive and disc, but nearly every caller has only a
// drive index. Answering those with whatever was cached for that drive shows
// one disc's titles for another — it was only ever safe because a disc change
// clears the cache, and that is an event.
func TestACachedScanIsNotServedForADifferentDisc(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{})
	orch.cacheScanFor(1, "RAMBO_DISC2", scanOf("RAMBO_DISC2", 3))

	// The drive now holds something else, and the cache was never cleared.
	orch.SetDriveDisc(1, "INVICTUS")

	if got := orch.GetCachedScanByDrive(1); got != nil {
		t.Errorf("a scan of INVICTUS was answered with %d titles from %q", len(got.Titles), got.DiscName)
	}
}

// The disc that was actually scanned still gets its scan.
func TestACachedScanIsServedForItsOwnDisc(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{})
	orch.cacheScanFor(1, "RAMBO_DISC2", scanOf("RAMBO_DISC2", 3))

	orch.SetDriveDisc(1, "RAMBO_DISC2")

	got := orch.GetCachedScanByDrive(1)
	if got == nil || len(got.Titles) != 3 {
		t.Errorf("GetCachedScanByDrive = %v, want the disc's own 3-title scan", got)
	}
}

// A scan of a repaired copy is a folder scan, and a folder scan names itself
// after the copied BDMV rather than the disc. Filed under that name it is
// unreachable to everyone asking about the disc — which is everyone — and the
// page loses the titles a salvage just spent hours recovering.
func TestASalvagedScanIsFiledUnderTheDiscNotTheFolder(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{})

	// What ScanSource makes of a copied folder.
	orch.cacheScanFor(1, "RAMBO_DISC2", scanOf("BDMV", 2))
	orch.SetDriveDisc(1, "RAMBO_DISC2")

	got := orch.GetCachedScanByDrive(1)
	if got == nil {
		t.Fatal("the salvaged scan cannot be found under the disc it came from")
	}
	if len(got.Titles) != 2 {
		t.Errorf("got %d titles, want the 2 the salvage recovered", len(got.Titles))
	}
}

// Before the drive has said what it holds, the old behaviour stands: there is
// nothing better to answer with, and refusing would lose the titles on a page
// loaded during startup.
func TestAnUnreportedDriveStillGetsItsOnlyCachedScan(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{})
	orch.cacheScanFor(1, "RAMBO_DISC2", scanOf("RAMBO_DISC2", 3))

	if got := orch.GetCachedScanByDrive(1); got == nil {
		t.Error("a drive that has not reported a disc lost its cached scan")
	}
}

// One drive's cache must never answer for another.
func TestDrivesDoNotShareCachedScans(t *testing.T) {
	orch := NewOrchestrator(OrchestratorDeps{})
	orch.cacheScanFor(1, "RAMBO_DISC2", scanOf("RAMBO_DISC2", 3))
	orch.SetDriveDisc(1, "RAMBO_DISC2")
	orch.SetDriveDisc(2, "RAMBO_DISC2")

	if got := orch.GetCachedScanByDrive(2); got != nil {
		t.Error("drive 2 was answered from drive 1's cache")
	}
}
