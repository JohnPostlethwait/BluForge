package drivemanager

import (
	"context"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// A confirmed physical removal must reset the drive to empty, forget the disc
// so the poller does not keep believing it is there, and emit an eject event so
// the running scan/rip can be cancelled and the mount released.
func TestNotePhysicalGoneEjectsDisc(t *testing.T) {
	var events []DriveEvent
	m := NewManager(&scriptedExecutor{polls: [][]makemkv.DriveInfo{withDisc("RAMBO")}}, func(ev DriveEvent) {
		events = append(events, ev)
	})
	m.PollOnce(context.Background())
	events = nil // drop the insert from the seed poll

	m.notePhysicalGone("/dev/sr1", false)

	if got := countType(events, EventDiscEjected); got != 1 {
		t.Fatalf("want 1 disc_ejected event, got %d (%v)", got, events)
	}
	dsm := m.GetDrive(1)
	if dsm == nil {
		t.Fatalf("drive 1 should still be known")
	}
	if dsm.State() != StateEmpty {
		t.Fatalf("drive should be reset to empty, got %v", dsm.State())
	}
	if dsm.DiscName() != "" {
		t.Fatalf("disc name should be cleared, got %q", dsm.DiscName())
	}
}

// When the device node itself has vanished, the event is a disconnect, not an
// eject.
func TestNotePhysicalGoneDisconnectWhenDeviceVanished(t *testing.T) {
	var events []DriveEvent
	m := NewManager(&scriptedExecutor{polls: [][]makemkv.DriveInfo{withDisc("RAMBO")}}, func(ev DriveEvent) {
		events = append(events, ev)
	})
	m.PollOnce(context.Background())
	events = nil

	m.notePhysicalGone("/dev/sr1", true)

	if got := countType(events, EventDriveDisconnect); got != 1 {
		t.Fatalf("want 1 drive_disconnect event, got %d (%v)", got, events)
	}
}

// An unknown device path is ignored: no event, no panic.
func TestNotePhysicalGoneIgnoresUnknownDevice(t *testing.T) {
	var events []DriveEvent
	m := NewManager(&scriptedExecutor{polls: [][]makemkv.DriveInfo{withDisc("RAMBO")}}, func(ev DriveEvent) {
		events = append(events, ev)
	})
	m.PollOnce(context.Background())
	events = nil

	m.notePhysicalGone("/dev/sr9", false)

	if len(events) != 0 {
		t.Fatalf("unknown device should emit nothing, got %v", events)
	}
}
