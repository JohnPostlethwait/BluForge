package workflow

import (
	"testing"
)

// A symlink recovery is a tree of links pointing into the disc's mount. The
// mount is now taken down the moment the disc leaves the drive, which is what
// stops a live filesystem on absent media wedging the drive — but it also makes
// those links dangle.
//
// SetDriveDisc un-retires a record when the same disc label comes back, so
// without this the drive would go straight back to reading a link tree whose
// mount is gone. A copy on disk does not depend on the mount and is kept.
func TestDroppingEphemeralRecoveriesLeavesRealCopiesAlone(t *testing.T) {
	o := &Orchestrator{recovered: map[int]*recoveredDisc{}}

	o.recovered[1] = &recoveredDisc{
		dir:        t.TempDir(),
		discLabels: []string{"A_DISC"},
		ephemeral:  true,
	}
	o.recovered[2] = &recoveredDisc{
		dir:        t.TempDir(),
		discLabels: []string{"OTHER_DISC"},
		ephemeral:  false,
	}

	o.DropEphemeralForDrive(1)
	o.DropEphemeralForDrive(2)

	if _, ok := o.recovered[1]; ok {
		t.Error("the link tree survived its disc leaving; its links point at an unmounted disc")
	}
	if _, ok := o.recovered[2]; !ok {
		t.Error("a real copy on disk was dropped, though it does not depend on the mount")
	}
}

// A rip is still reading through the links. Dropping the record out from under
// it would strand the job, and the disc is already gone either way — the rip
// fails on its own and releases its claim.
func TestAnEphemeralRecoveryInUseIsNotDropped(t *testing.T) {
	o := &Orchestrator{recovered: map[int]*recoveredDisc{}}
	o.recovered[1] = &recoveredDisc{
		dir:        t.TempDir(),
		discLabels: []string{"A_DISC"},
		ephemeral:  true,
		refCount:   1,
	}

	o.DropEphemeralForDrive(1)

	rec, ok := o.recovered[1]
	if !ok {
		t.Fatal("a link tree with a rip in flight was dropped")
	}
	if !rec.retired {
		t.Error("a link tree with a rip in flight was left current; it must not be read again")
	}
}

// Nothing recovered for this drive is the ordinary case — every disc event
// calls this.
func TestDroppingWithNoRecoveryIsQuiet(t *testing.T) {
	o := &Orchestrator{recovered: map[int]*recoveredDisc{}}
	o.DropEphemeralForDrive(7)
}
