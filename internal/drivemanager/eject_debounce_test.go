package drivemanager

import (
	"context"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// scriptedExecutor returns a different drive listing on each poll.
type scriptedExecutor struct {
	polls [][]makemkv.DriveInfo
	n     int
}

func (s *scriptedExecutor) ListDrives(context.Context) ([]makemkv.DriveInfo, error) {
	if s.n >= len(s.polls) {
		return s.polls[len(s.polls)-1], nil
	}
	out := s.polls[s.n]
	s.n++
	return out, nil
}

func (s *scriptedExecutor) ScanDisc(context.Context, int) (*makemkv.DiscScan, error) {
	return nil, nil
}

func withDisc(name string) []makemkv.DriveInfo {
	return []makemkv.DriveInfo{{Index: 1, Flags: 2, DriveName: "BD-RE", DiscName: name, DevicePath: "/dev/sr1"}}
}

func withoutDisc() []makemkv.DriveInfo {
	return []makemkv.DriveInfo{{Index: 1, Flags: 0, DriveName: "BD-RE", DiscName: "", DevicePath: "/dev/sr1"}}
}

func collectEvents(t *testing.T, polls [][]makemkv.DriveInfo) []DriveEvent {
	t.Helper()
	var events []DriveEvent
	m := NewManager(&scriptedExecutor{polls: polls}, func(ev DriveEvent) {
		events = append(events, ev)
	})
	for range polls {
		m.PollOnce(context.Background())
	}
	return events
}

func countType(events []DriveEvent, typ EventType) int {
	n := 0
	for _, ev := range events {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// Observed in production: opening the drive for a backup produced one poll
// reporting no disc, which fired disc_ejected and wiped the user's release
// selection — the disc had never left the drive. A single reading is not
// evidence of an eject.
func TestTransientEmptyPollDoesNotEject(t *testing.T) {
	events := collectEvents(t, [][]makemkv.DriveInfo{
		withDisc("STRANGER_THINGS"),
		withoutDisc(), // the drive is busy, not empty
		withDisc("STRANGER_THINGS"),
		withDisc("STRANGER_THINGS"),
	})

	if n := countType(events, EventDiscEjected); n != 0 {
		t.Errorf("emitted %d eject events for a disc that never left the drive", n)
	}
	// And no phantom re-insert of a disc that was there all along.
	if n := countType(events, EventDiscInserted); n != 1 {
		t.Errorf("emitted %d insert events, want 1 (the original insert)", n)
	}
}

// A real eject must still be reported, just after enough polls to be sure.
func TestSustainedAbsenceEjects(t *testing.T) {
	events := collectEvents(t, [][]makemkv.DriveInfo{
		withDisc("STRANGER_THINGS"),
		withoutDisc(),
		withoutDisc(),
		withoutDisc(),
	})

	if n := countType(events, EventDiscEjected); n != 1 {
		t.Errorf("emitted %d eject events for a genuinely removed disc, want 1", n)
	}
}

// Swapping discs is a real change and must not be swallowed by the debounce.
func TestDiscSwapStillReported(t *testing.T) {
	events := collectEvents(t, [][]makemkv.DriveInfo{
		withDisc("FIRST_DISC"),
		withDisc("SECOND_DISC"),
	})

	if n := countType(events, EventDiscInserted); n != 2 {
		t.Errorf("emitted %d insert events across a disc swap, want 2", n)
	}
}

// After a transient reading the drive must still be considered occupied, or the
// next poll reports a fresh insert and invalidates cached state anyway.
func TestTransientAbsenceLeavesDriveDetected(t *testing.T) {
	polls := [][]makemkv.DriveInfo{
		withDisc("STRANGER_THINGS"),
		withoutDisc(),
	}
	m := NewManager(&scriptedExecutor{polls: polls}, func(DriveEvent) {})
	for range polls {
		m.PollOnce(context.Background())
	}

	drv := m.GetDrive(1)
	if drv == nil {
		t.Fatal("drive disappeared")
	}
	if drv.State() != StateDetected {
		t.Errorf("state = %q, want %q after a single empty reading", drv.State(), StateDetected)
	}
	if drv.DiscName() != "STRANGER_THINGS" {
		t.Errorf("disc name = %q, want it retained", drv.DiscName())
	}
}
