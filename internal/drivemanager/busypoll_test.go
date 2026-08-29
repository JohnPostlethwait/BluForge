package drivemanager

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// busyExecutor is a drive lister that is currently in use: TryListDrives
// declines, and a caller that insists on ListDrives blocks until released.
type busyExecutor struct {
	busy     atomic.Bool
	blocked  atomic.Int32
	tried    atomic.Int32
	released chan struct{}
	drives   []makemkv.DriveInfo
}

func (b *busyExecutor) ListDrives(_ context.Context) ([]makemkv.DriveInfo, error) {
	if b.busy.Load() {
		// What waiting on the executor mutex looks like. Bounded so a caller
		// that waits fails the test rather than hanging it.
		b.blocked.Add(1)
		select {
		case <-b.released:
		case <-time.After(500 * time.Millisecond):
		}
	}
	return b.drives, nil
}

func (b *busyExecutor) TryListDrives(_ context.Context) ([]makemkv.DriveInfo, bool, error) {
	b.tried.Add(1)
	if b.busy.Load() {
		return nil, false, nil
	}
	return b.drives, true, nil
}

func (b *busyExecutor) ScanDisc(_ context.Context, _ int) (*makemkv.DiscScan, error) {
	return nil, errors.New("not used")
}

func newBusyExecutor() *busyExecutor {
	return &busyExecutor{
		released: make(chan struct{}),
		drives: []makemkv.DriveInfo{{
			Index: 0, State: makemkv.DriveStateInserted,
			DriveName: "BD-RE", DiscName: "A_DISC", DevicePath: "/dev/sr0",
		}},
	}
}

// Every makemkvcon call shares one mutex, deliberately: an unlocked poll every
// five seconds against a drive being read by ddrescue took a rescue from
// 14 MB/s to 2.4 MB/s. The poller must keep out of the way.
//
// It did that by blocking, which parks the poller for the whole of a rip and
// then fires the moment the lock frees — with the next poll immediately behind
// it. Skipping the cycle keeps out of the way just as well and does neither.
func TestPollSkipsRatherThanBlockingWhileTheDriveIsBusy(t *testing.T) {
	exec := newBusyExecutor()
	exec.busy.Store(true)

	mgr := NewManager(exec, func(DriveEvent) {})

	done := make(chan struct{})
	go func() {
		mgr.PollOnce(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(exec.released)
		t.Fatal("PollOnce blocked while the drive was in use; it must skip the cycle")
	}

	if n := exec.blocked.Load(); n != 0 {
		t.Errorf("the poll waited on the busy lister %d time(s); it must not wait at all", n)
	}
	if n := exec.tried.Load(); n == 0 {
		t.Error("the poll never asked whether the drive was free")
	}
}

// A skipped poll must change nothing: no events, and no drive invented or
// forgotten on the strength of a listing that never happened.
func TestASkippedPollLeavesDriveStateAlone(t *testing.T) {
	exec := newBusyExecutor()
	mgr := NewManager(exec, func(DriveEvent) {})

	// One real poll so there is state to disturb.
	mgr.PollOnce(context.Background())
	if len(mgr.GetAllDrives()) != 1 {
		t.Fatalf("setup: expected 1 drive")
	}

	var events []DriveEvent
	mgr2 := NewManager(exec, func(e DriveEvent) { events = append(events, e) })
	mgr2.PollOnce(context.Background())
	events = nil

	exec.busy.Store(true)
	mgr2.PollOnce(context.Background())
	mgr2.PollOnce(context.Background())

	if len(events) != 0 {
		t.Errorf("a skipped poll emitted %d event(s): %+v", len(events), events)
	}
	if got := len(mgr2.GetAllDrives()); got != 1 {
		t.Errorf("GetAllDrives returned %d drives after skipped polls, want 1 — "+
			"a skipped poll must not read as the drive disappearing", got)
	}
}

// An executor with no TryListDrives — any other implementation, and the tests
// that use one — must keep working through the ordinary path.
func TestAListerWithoutTryStillPolls(t *testing.T) {
	mock := &mockExecutor{drives: []makemkv.DriveInfo{{
		Index: 0, State: makemkv.DriveStateInserted,
		DriveName: "BD-RE", DiscName: "A_DISC", DevicePath: "/dev/sr0",
	}}}
	mgr := NewManager(mock, func(DriveEvent) {})

	mgr.PollOnce(context.Background())

	if got := len(mgr.GetAllDrives()); got != 1 {
		t.Errorf("GetAllDrives returned %d drives, want 1", got)
	}
}
