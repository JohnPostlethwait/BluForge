package drivemanager

import (
	"strings"
	"testing"
)

func TestStateRecoveringIsDistinctFromDetected(t *testing.T) {
	dsm := NewDriveState(0, "/dev/sr0")

	dsm.SetState(StateRecovering)
	if dsm.State() != StateRecovering {
		t.Errorf("State() = %q, want %q", dsm.State(), StateRecovering)
	}
	if StateRecovering == StateDetected || StateRecovering == StateEmpty {
		t.Error("StateRecovering collides with an existing state")
	}
}

// The diagnosis exists so the message names the cause. A user reading it should
// not have to know that makemkvcon enumerates drives through /dev/sg*.
func TestDescribeOpticalAccessNamesTheGroupProblem(t *testing.T) {
	got := describeOpticalAccess(opticalAccess{
		nodesFound:    []string{"/dev/sg0", "/dev/sg1"},
		readable:      0,
		running:       "99:100",
		owningGroups:  []string{"disk(6)"},
		processGroups: []string{"100"},
	})

	if got == "" {
		t.Fatal("describeOpticalAccess returned no diagnosis when no node was readable")
	}
	for _, want := range []string{"/dev/sg", "disk(6)", "99:100"} {
		if !strings.Contains(got, want) {
			t.Errorf("diagnosis %q does not mention %q", got, want)
		}
	}
}

// Readable nodes mean there is nothing to warn about — a spurious warning on
// every start would train users to ignore it.
func TestDescribeOpticalAccessSilentWhenReadable(t *testing.T) {
	got := describeOpticalAccess(opticalAccess{
		nodesFound: []string{"/dev/sg0"},
		readable:   1,
	})
	if got != "" {
		t.Errorf("describeOpticalAccess returned %q, want silence when nodes are readable", got)
	}
}

// No /dev/sg* at all is a different situation — no devices passed into the
// container — and must not be reported as a group problem.
func TestDescribeOpticalAccessSilentWhenNoNodes(t *testing.T) {
	if got := describeOpticalAccess(opticalAccess{}); got != "" {
		t.Errorf("describeOpticalAccess returned %q, want silence when no nodes exist", got)
	}
}
