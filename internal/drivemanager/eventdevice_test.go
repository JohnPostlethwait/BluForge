package drivemanager

import (
	"context"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// Every event that means "the media in this drive changed" has to say which
// device it happened to.
//
// The disc mount is keyed by device path, and a mount that outlives its disc is
// what wedges the drive: the kernel keeps a live filesystem on media that is no
// longer there, every I/O fails, and the bridge resets until it stops answering
// at all. The handler cannot release the right mount from a drive index alone —
// and on a disconnect the state machine it would have looked the path up from
// has already been removed.
func TestDiscEventsCarryTheDevicePath(t *testing.T) {
	cm := newClockedManager(t, [][]makemkv.DriveInfo{
		withDisc("DISC_ONE"),
		withDisc("DISC_TWO"),
		withoutDisc(),
	})

	cm.poll(0)                    // inserted
	cm.poll(5 * time.Second)      // swapped: another insert
	cm.poll(ejectConfirmDuration) // gone long enough to count as an eject

	for _, ev := range *cm.events {
		switch ev.Type {
		case EventDiscInserted, EventDiscEjected:
			if ev.DevicePath != "/dev/sr1" {
				t.Errorf("%s event carried device path %q, want /dev/sr1", ev.Type, ev.DevicePath)
			}
		}
	}
}

// A disconnect is the case that cannot be recovered after the fact: the drive
// is deleted from the map, so its device path is gone with it.
func TestADisconnectCarriesTheDevicePath(t *testing.T) {
	var events []DriveEvent
	mock := &mockExecutor{drives: []makemkv.DriveInfo{
		{Index: 1, State: makemkv.DriveStateInserted, DriveName: "BD-RE", DiscName: "A_DISC", Flags: 2, DevicePath: "/dev/sr1"},
	}}
	mgr := NewManager(mock, func(e DriveEvent) { events = append(events, e) })
	now := time.Date(2026, 9, 3, 4, 0, 0, 0, time.UTC)
	mgr.now = func() time.Time { return now }

	mgr.PollOnce(context.Background())
	mock.drives = nil
	events = nil

	mgr.PollOnce(context.Background())
	now = now.Add(driveGoneConfirmDuration)
	mgr.PollOnce(context.Background())

	for _, ev := range events {
		if ev.Type != EventDriveDisconnect {
			continue
		}
		if ev.DevicePath != "/dev/sr1" {
			t.Errorf("disconnect event carried device path %q, want /dev/sr1", ev.DevicePath)
		}
		return
	}
	t.Fatal("no disconnect event was emitted")
}
