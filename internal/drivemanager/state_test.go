package drivemanager

import (
	"testing"
)

func TestNewDriveStateStartsEmpty(t *testing.T) {
	drive := NewDriveState(0, "/dev/sr0")

	if drive.State() != StateEmpty {
		t.Fatalf("expected initial state %q, got %q", StateEmpty, drive.State())
	}
	if drive.Index() != 0 {
		t.Errorf("expected index 0, got %d", drive.Index())
	}
	if drive.DevicePath() != "/dev/sr0" {
		t.Errorf("expected device path %q, got %q", "/dev/sr0", drive.DevicePath())
	}
}

func TestSetState(t *testing.T) {
	drive := NewDriveState(0, "/dev/sr0")

	drive.SetState(StateDetected)
	if drive.State() != StateDetected {
		t.Fatalf("expected state %q, got %q", StateDetected, drive.State())
	}

	drive.SetState(StateEmpty)
	if drive.State() != StateEmpty {
		t.Fatalf("expected state %q, got %q", StateEmpty, drive.State())
	}
}

func TestForceReset(t *testing.T) {
	drive := NewDriveState(1, "/dev/sr1")

	drive.SetState(StateDetected)
	drive.SetDiscName("Some Disc")

	drive.ForceReset()

	if drive.State() != StateEmpty {
		t.Fatalf("expected state %q after ForceReset, got %q", StateEmpty, drive.State())
	}
	if drive.DiscName() != "" {
		t.Fatalf("expected empty disc name after ForceReset, got %q", drive.DiscName())
	}
}

func TestDriveNameAccessors(t *testing.T) {
	drive := NewDriveState(0, "/dev/sr0")

	drive.SetDriveName("BD-RE ASUS BW-16D1HT")
	if drive.DriveName() != "BD-RE ASUS BW-16D1HT" {
		t.Errorf("expected drive name %q, got %q", "BD-RE ASUS BW-16D1HT", drive.DriveName())
	}

	drive.SetDiscName("MOVIE_DISC")
	if drive.DiscName() != "MOVIE_DISC" {
		t.Errorf("expected disc name %q, got %q", "MOVIE_DISC", drive.DiscName())
	}
}
