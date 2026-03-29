package drivemanager

import (
	"testing"
)

func TestValidTransitions(t *testing.T) {
	tests := []struct {
		name  string
		from  DriveState
		to    DriveState
		valid bool
	}{
		// Valid transitions
		{name: "Empty->Detected", from: StateEmpty, to: StateDetected, valid: true},
		{name: "Detected->Scanning", from: StateDetected, to: StateScanning, valid: true},
		{name: "Scanning->Identified", from: StateScanning, to: StateIdentified, valid: true},
		{name: "Scanning->NotFound", from: StateScanning, to: StateNotFound, valid: true},
		{name: "Identified->Ready", from: StateIdentified, to: StateReady, valid: true},
		{name: "Ready->Ripping", from: StateReady, to: StateRipping, valid: true},
		{name: "NotFound->Ripping", from: StateNotFound, to: StateRipping, valid: true},
		{name: "Ripping->Organizing", from: StateRipping, to: StateOrganizing, valid: true},
		{name: "Organizing->Complete", from: StateOrganizing, to: StateComplete, valid: true},
		{name: "Complete->Ejecting", from: StateComplete, to: StateEjecting, valid: true},
		{name: "Ejecting->Empty", from: StateEjecting, to: StateEmpty, valid: true},
		// Invalid transitions
		{name: "Empty->Ripping", from: StateEmpty, to: StateRipping, valid: false},
		{name: "Ripping->Empty", from: StateRipping, to: StateEmpty, valid: false},
		{name: "Complete->Ripping", from: StateComplete, to: StateRipping, valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsValidTransition(tc.from, tc.to)
			if got != tc.valid {
				t.Errorf("IsValidTransition(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.valid)
			}
		})
	}
}

func TestDriveStateTransition(t *testing.T) {
	drive := NewDriveState(0, "/dev/sr0")

	if drive.State() != StateEmpty {
		t.Fatalf("expected initial state %q, got %q", StateEmpty, drive.State())
	}

	if err := drive.TransitionTo(StateDetected); err != nil {
		t.Fatalf("expected valid transition Empty->Detected, got error: %v", err)
	}

	if drive.State() != StateDetected {
		t.Fatalf("expected state %q after transition, got %q", StateDetected, drive.State())
	}

	if err := drive.TransitionTo(StateRipping); err == nil {
		t.Fatal("expected error for invalid transition Detected->Ripping, got nil")
	}

	// State must remain Detected after the rejected transition.
	if drive.State() != StateDetected {
		t.Fatalf("state should remain %q after rejected transition, got %q", StateDetected, drive.State())
	}
}

func TestForceReset(t *testing.T) {
	drive := NewDriveState(1, "/dev/sr1")

	if err := drive.TransitionTo(StateDetected); err != nil {
		t.Fatalf("transition to Detected failed: %v", err)
	}
	if err := drive.TransitionTo(StateScanning); err != nil {
		t.Fatalf("transition to Scanning failed: %v", err)
	}
	drive.SetDiscName("Some Disc")

	drive.ForceReset()

	if drive.State() != StateEmpty {
		t.Fatalf("expected state %q after ForceReset, got %q", StateEmpty, drive.State())
	}
	if drive.DiscName() != "" {
		t.Fatalf("expected empty disc name after ForceReset, got %q", drive.DiscName())
	}
}
