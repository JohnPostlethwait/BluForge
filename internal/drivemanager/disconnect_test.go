package drivemanager

import (
	"context"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

func countEvents(events []DriveEvent, t EventType) int {
	n := 0
	for _, e := range events {
		if e.Type == t {
			n++
		}
	}
	return n
}

// A drive that is unplugged was announced as disconnected on every subsequent
// poll, for as long as the process ran. Nothing removed it from the map, so
// each pass found it missing again and said so again — an SSE event every few
// seconds, forever, for a drive that left once.
//
// It also stayed in GetAllDrives, so the dashboard kept a card for hardware
// that is not there.
func TestDisconnectIsAnnouncedOnceNotOnEveryPoll(t *testing.T) {
	var events []DriveEvent
	mock := &mockExecutor{drives: []makemkv.DriveInfo{
		{Index: 0, State: makemkv.DriveStateInserted, DriveName: "BD-RE ASUS", DiscName: "A_DISC", Flags: 1, DevicePath: "/dev/sr0"},
	}}
	mgr := NewManager(mock, func(e DriveEvent) { events = append(events, e) })

	mgr.PollOnce(context.Background())
	if countEvents(events, EventDiscInserted) != 1 {
		t.Fatalf("setup: expected the disc to be detected, got %+v", events)
	}

	// The drive is unplugged: makemkvcon stops listing it entirely.
	mock.drives = nil
	events = nil

	mgr.PollOnce(context.Background())
	if got := countEvents(events, EventDriveDisconnect); got != 1 {
		t.Fatalf("first poll after removal: got %d disconnect events, want 1", got)
	}

	events = nil
	mgr.PollOnce(context.Background())
	mgr.PollOnce(context.Background())
	mgr.PollOnce(context.Background())

	if got := countEvents(events, EventDriveDisconnect); got != 0 {
		t.Errorf("got %d further disconnect events over three polls, want 0", got)
	}
}

// The drive is gone; it must not keep occupying a card on the dashboard.
func TestADisconnectedDriveIsNoLongerListed(t *testing.T) {
	mock := &mockExecutor{drives: []makemkv.DriveInfo{
		{Index: 0, State: makemkv.DriveStateInserted, DriveName: "BD-RE ASUS", DiscName: "A_DISC", Flags: 1, DevicePath: "/dev/sr0"},
	}}
	mgr := NewManager(mock, func(DriveEvent) {})

	mgr.PollOnce(context.Background())
	if len(mgr.GetAllDrives()) != 1 {
		t.Fatalf("setup: expected 1 drive")
	}

	mock.drives = nil
	mgr.PollOnce(context.Background())

	if got := len(mgr.GetAllDrives()); got != 0 {
		t.Errorf("GetAllDrives returned %d drives after the only one was unplugged, want 0", got)
	}
}

// Unplugging and replugging has to work: the drive comes back as a new arrival
// with its disc detected, not silently absent because the map still holds it.
func TestAReconnectedDriveIsDetectedAgain(t *testing.T) {
	var events []DriveEvent
	present := []makemkv.DriveInfo{
		{Index: 0, State: makemkv.DriveStateInserted, DriveName: "BD-RE ASUS", DiscName: "A_DISC", Flags: 1, DevicePath: "/dev/sr0"},
	}
	mock := &mockExecutor{drives: present}
	mgr := NewManager(mock, func(e DriveEvent) { events = append(events, e) })

	mgr.PollOnce(context.Background())
	mock.drives = nil
	mgr.PollOnce(context.Background())

	events = nil
	mock.drives = present
	mgr.PollOnce(context.Background())

	if got := countEvents(events, EventDiscInserted); got != 1 {
		t.Errorf("got %d insert events on reconnect, want 1 (events: %+v)", got, events)
	}
	if got := len(mgr.GetAllDrives()); got != 1 {
		t.Errorf("GetAllDrives returned %d drives after reconnect, want 1", got)
	}
}
