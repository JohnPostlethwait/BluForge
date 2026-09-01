package makemkv

import (
	"context"
	"errors"
	"testing"
)

// makemkvcon exits non-zero for benign reasons — an empty drive is one — and
// still names every drive it found, so a failed listing that carries DRV lines
// is normally taken at its word.
//
// A listing BluForge killed itself is not that. The thirty second timeout fires
// while makemkvcon is still enumerating, so the DRV lines it managed to emit
// are a prefix, not a list: the drives it had not reached yet are missing
// because it ran out of time, and the poller would take every one of them for
// unplugged.
func TestAKilledListingIsNotAPartialSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	partial := `DRV:0,2,999,1,"BD-RE HL-DT-ST BD-RE  WH16NS40","DEADPOOL_2","/dev/sr0"` + "\n"
	ex := NewExecutor(WithRunner(&mockCmdRunner{output: partial, err: errors.New("signal: killed")}))

	drives, err := ex.ListDrives(ctx)
	if err == nil {
		t.Fatalf("a killed listing reported success with %d drives; every drive it did not reach would be taken for unplugged", len(drives))
	}
}

// The benign case still has to work: makemkvcon exiting non-zero on its own,
// having listed everything it found.
func TestAFailedListingThatRanToCompletionIsStillUsed(t *testing.T) {
	ex := NewExecutor(WithRunner(&mockCmdRunner{output: twoDriverOutput, err: errors.New("exit status 1")}))

	drives, err := ex.ListDrives(context.Background())
	if err != nil {
		t.Fatalf("ListDrives: %v", err)
	}
	if len(drives) != 2 {
		t.Errorf("got %d drives, want 2", len(drives))
	}
}
