package drivemanager

import (
	"context"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// Plenty of discs carry no volume label. Presence was inferred from the disc
// name and the media flags, so an unlabelled disc read as an empty drive: the
// page said "Insert a disc to begin" with one already in, and nothing could be
// scanned or ripped because as far as BluForge knew there was nothing there.
//
// MakeMKV says so directly in the DRV state field.
func TestAnUnlabelledDiscIsDetected(t *testing.T) {
	var events []DriveEvent
	mock := &mockExecutor{drives: []makemkv.DriveInfo{{
		Index:      0,
		State:      makemkv.DriveStateInserted,
		Flags:      0,
		DriveName:  "BD-RE ASUS BW-16D1HT",
		DiscName:   "",
		DevicePath: "/dev/sr0",
	}}}
	mgr := NewManager(mock, func(e DriveEvent) { events = append(events, e) })

	mgr.PollOnce(context.Background())

	if countEvents(events, EventDiscInserted) != 1 {
		t.Errorf("no disc_inserted for an unlabelled disc; events: %+v", events)
	}
	drv := mgr.GetDrive(0)
	if drv == nil {
		t.Fatal("drive 0 is not known")
	}
	if got := drv.State(); got != StateDetected {
		t.Errorf("drive state = %q, want %q", got, StateDetected)
	}
}

// An empty drive must still read as empty — the fix must not turn every drive
// into one holding a disc.
func TestAnEmptyDriveIsStillEmpty(t *testing.T) {
	var events []DriveEvent
	mock := &mockExecutor{drives: []makemkv.DriveInfo{{
		Index:      0,
		State:      makemkv.DriveStateEmptyClosed,
		DriveName:  "BD-RE ASUS BW-16D1HT",
		DevicePath: "/dev/sr0",
	}}}
	mgr := NewManager(mock, func(e DriveEvent) { events = append(events, e) })

	mgr.PollOnce(context.Background())

	if countEvents(events, EventDiscInserted) != 0 {
		t.Errorf("an empty drive reported a disc; events: %+v", events)
	}
	if got := mgr.GetDrive(0).State(); got != StateEmpty {
		t.Errorf("drive state = %q, want %q", got, StateEmpty)
	}
}

// A slot with no hardware is still skipped, so it never becomes a card on the
// dashboard nor a candidate for a disconnect event.
func TestAPhantomSlotIsNotADrive(t *testing.T) {
	mock := &mockExecutor{drives: []makemkv.DriveInfo{
		{Index: 0, State: makemkv.DriveStateInserted, DriveName: "BD-RE", DiscName: "A_DISC", DevicePath: "/dev/sr0"},
		{Index: 1, State: makemkv.DriveStateNoDrive},
	}}
	mgr := NewManager(mock, func(DriveEvent) {})

	mgr.PollOnce(context.Background())

	if got := len(mgr.GetAllDrives()); got != 1 {
		t.Errorf("GetAllDrives returned %d drives, want 1 — the phantom slot was counted", got)
	}
}

// A real drive that momentarily reports no name is not an unplugged drive. It
// still has its device path, and treating it as gone emitted a spurious
// disconnect and dropped it from the dashboard.
func TestADriveThatMomentarilyReportsNoNameIsNotDisconnected(t *testing.T) {
	named := makemkv.DriveInfo{
		Index: 0, State: makemkv.DriveStateEmptyClosed,
		DriveName: "BD-RE ASUS", DevicePath: "/dev/sr0",
	}
	mock := &mockExecutor{drives: []makemkv.DriveInfo{named}}

	var events []DriveEvent
	mgr := NewManager(mock, func(e DriveEvent) { events = append(events, e) })
	mgr.PollOnce(context.Background())

	// Same drive, same slot, same device — but this poll gave no name.
	events = nil
	mock.drives = []makemkv.DriveInfo{{
		Index: 0, State: makemkv.DriveStateEmptyClosed, DevicePath: "/dev/sr0",
	}}
	mgr.PollOnce(context.Background())

	if got := countEvents(events, EventDriveDisconnect); got != 0 {
		t.Errorf("got %d disconnect events for a drive that only lost its name", got)
	}
	if got := len(mgr.GetAllDrives()); got != 1 {
		t.Errorf("GetAllDrives returned %d drives, want 1", got)
	}
}
