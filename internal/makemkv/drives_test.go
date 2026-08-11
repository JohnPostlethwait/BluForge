package makemkv

import (
	"context"
	"errors"
	"testing"
)

// Zero drives plus MSG:5042 under Docker almost always means the process is not
// in the group owning /dev/sg*, not that the hardware is missing. Saying so is
// the difference between a five-minute fix and an afternoon.
func TestListDrivesNoDrivesGivesActionableError(t *testing.T) {
	runner := &recordingRunner{
		output: `MSG:5042,0,0,"The program can't find any usable optical drives.","%1","The program can't find any usable optical drives."`,
		err:    errors.New("exit status 1"),
	}
	ex := NewExecutor(WithRunner(runner))

	_, err := ex.ListDrives(context.Background())
	if err == nil {
		t.Fatal("ListDrives succeeded with no drives, want error")
	}
	if !errors.Is(err, ErrNoOpticalDrives) {
		t.Errorf("error = %v, want it to wrap ErrNoOpticalDrives", err)
	}
}

// 5042 alongside drives that were actually enumerated is noise and must not
// turn a working listing into a failure.
func TestListDrivesIgnores5042WhenDrivesFound(t *testing.T) {
	runner := &recordingRunner{
		output: `MSG:5042,0,0,"The program can't find any usable optical drives.","%1","The program can't find any usable optical drives."` + "\n" + twoDriverOutput,
	}
	ex := NewExecutor(WithRunner(runner))

	drives, err := ex.ListDrives(context.Background())
	if err != nil {
		t.Fatalf("ListDrives returned error despite finding drives: %v", err)
	}
	if len(drives) != 2 {
		t.Errorf("found %d drives, want 2", len(drives))
	}
}

// Recovery needs the device path to mount the disc for inspection.
func TestDevicePathForDriveIsExported(t *testing.T) {
	runner := &recordingRunner{output: twoDriverOutput}
	ex := NewExecutor(WithRunner(runner))

	if got := ex.DevicePathForDrive(context.Background(), 1); got != "/dev/sr1" {
		t.Errorf("DevicePathForDrive(1) = %q, want /dev/sr1", got)
	}
	if got := ex.DevicePathForDrive(context.Background(), 99); got != "" {
		t.Errorf("DevicePathForDrive(99) = %q, want empty for an unknown drive", got)
	}
}
