package makemkv

import (
	"context"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/testutil"
)

// The drive poller shares one mutex with every other makemkvcon operation, and
// that is deliberate: an unlocked poll running every five seconds against a
// drive being read by ddrescue took a rescue from 14 MB/s down to 2.4 MB/s.
//
// But blocking on the lock is not the only way to stay out of the way. A poll
// that waits behind a three-hour rip is worse than one that does not happen:
// nothing is learned either way, and the waiting poll is queued to fire the
// instant the lock releases, which is the burst the eject debounce exists to
// survive.
//
// TryListDrives declines instead of waiting.
func TestTryListDrivesDeclinesWhileTheDriveIsBusy(t *testing.T) {
	exec := NewExecutor(WithRunner(&mockCmdRunner{output: testutil.SampleDriveListOutput}))

	exec.LockDrive()
	defer exec.UnlockDrive()

	done := make(chan struct{})
	var ok bool
	var err error
	go func() {
		_, ok, err = exec.TryListDrives(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("TryListDrives blocked while the drive was held; it must return at once")
	}

	if ok {
		t.Error("TryListDrives claimed to have listed drives while the drive was held")
	}
	if err != nil {
		t.Errorf("a declined listing is not an error, got %v", err)
	}
}

// When nothing holds the drive it must behave exactly as ListDrives does.
func TestTryListDrivesListsWhenTheDriveIsFree(t *testing.T) {
	exec := NewExecutor(WithRunner(&mockCmdRunner{output: testutil.SampleDriveListOutput}))

	drives, ok, err := exec.TryListDrives(context.Background())
	if err != nil {
		t.Fatalf("TryListDrives: %v", err)
	}
	if !ok {
		t.Fatal("TryListDrives declined although nothing held the drive")
	}
	if len(drives) == 0 {
		t.Fatal("no drives returned")
	}

	want, err := exec.ListDrives(context.Background())
	if err != nil {
		t.Fatalf("ListDrives: %v", err)
	}
	if len(drives) != len(want) {
		t.Errorf("TryListDrives returned %d drives, ListDrives returned %d", len(drives), len(want))
	}
}

// Declining must release nothing it did not take: a second call once the drive
// is free has to succeed.
func TestTryListDrivesRecoversAfterTheDriveIsReleased(t *testing.T) {
	exec := NewExecutor(WithRunner(&mockCmdRunner{output: testutil.SampleDriveListOutput}))

	exec.LockDrive()
	if _, ok, _ := exec.TryListDrives(context.Background()); ok {
		t.Fatal("listed while held")
	}
	exec.UnlockDrive()

	if _, ok, err := exec.TryListDrives(context.Background()); !ok || err != nil {
		t.Errorf("ok=%v err=%v after release, want ok=true err=nil", ok, err)
	}
}
