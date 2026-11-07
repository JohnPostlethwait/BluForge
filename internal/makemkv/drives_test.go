package makemkv

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// discSrc addresses drive i by its node, as internal/drives does.
func discSrc(i int) Source { return DeviceSource(i, fmt.Sprintf("/dev/sr%d", i)) }

// Zero drives plus MSG:5042 under Docker almost always means the process is not
// in the group owning /dev/sg*, not that the hardware is missing. Saying so is
// the difference between a five-minute fix and an afternoon.
func TestIdentifyWithNoDrivesGivesActionableError(t *testing.T) {
	runner := &recordingRunner{
		output: `MSG:5042,0,0,"The program can't find any usable optical drives.","%1","The program can't find any usable optical drives."`,
		err:    errors.New("exit status 1"),
	}
	ex := NewExecutor(WithRunner(runner))

	_, err := ex.Identify(context.Background(), discSrc(0))
	if !errors.Is(err, ErrNoOpticalDrives) {
		t.Errorf("error = %v, want it to wrap ErrNoOpticalDrives", err)
	}
}

// Without isolation makemkvcon lists every drive; Identify picks this one out
// by its node and gives it BluForge's index.
func TestIdentifyPicksItsDriveFromAFullListing(t *testing.T) {
	runner := &recordingRunner{output: twoDriverOutput}
	ex := NewExecutor(WithRunner(runner))

	info, err := ex.Identify(context.Background(), DeviceSource(5, "/dev/sr1"))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if info.DriveName != "BD-RE ASUS BW-16D1HT" || info.DiscName != "AVENGERS_ENDGAME" || info.Index != 5 {
		t.Errorf("info = %+v, want the ASUS at /dev/sr1 as drive 5", info)
	}
}

// With isolation the listing sees only this drive, so its one line is it,
// whatever index makemkvcon gave it inside the private /dev.
func TestIsolatedIdentifyReadsTheOneDriveItCanSee(t *testing.T) {
	runner := &recordingRunner{output: `DRV:0,2,999,1,"BD-RE PIONEER BD-RW  BDR-XD08U 1.04","CINDERELLA_MAN","/dev/sr2"` + "\n"}
	ex := NewExecutor(WithRunner(runner), WithDriveIsolation(true))

	info, err := ex.Identify(context.Background(), discSrc(2))
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if info.Index != 2 || info.DiscName != "CINDERELLA_MAN" || info.DevicePath != "/dev/sr2" {
		t.Errorf("info = %+v", info)
	}
}

// A listing that names other drives but not this one has not identified it.
func TestIdentifyRefusesAListingWithoutItsDrive(t *testing.T) {
	runner := &recordingRunner{output: twoDriverOutput}
	ex := NewExecutor(WithRunner(runner))

	if _, err := ex.Identify(context.Background(), discSrc(7)); err == nil {
		t.Error("Identify returned another drive's line for /dev/sr7")
	}
}

// A drive is read only through internal/drives, which supplies its node. A
// bare index would have makemkvcon pick a drive by its own numbering.
func TestADiscWithoutADeviceIsRefused(t *testing.T) {
	ex := NewExecutor(WithRunner(&recordingRunner{}))
	if _, err := ex.ScanSource(context.Background(), DiscSource(0)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("scan err = %v, want ErrNoDevice", err)
	}
	if err := ex.StartRip(context.Background(), DiscSource(0), 0, "", t.TempDir(), nil, nil); !errors.Is(err, ErrNoDevice) {
		t.Errorf("rip err = %v, want ErrNoDevice", err)
	}
	if _, err := ex.Identify(context.Background(), DiscSource(0)); !errors.Is(err, ErrNoDevice) {
		t.Errorf("identify err = %v, want ErrNoDevice", err)
	}
}
