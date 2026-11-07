package drivemanager

import "testing"

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
