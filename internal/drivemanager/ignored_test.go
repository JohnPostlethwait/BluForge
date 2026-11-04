package drivemanager

import (
	"context"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

const (
	brokenASUS = "BD-RE ASUS BW-16D1HT 3.10 KLFO82D3028"
	goodLG     = "BD-RE HL-DT-ST BD-RE  WH16NS40 1.02 SIK9TH8A1234"
)

func asusAt(index int, path string, state int, disc string) makemkv.DriveInfo {
	return makemkv.DriveInfo{Index: index, State: state, DriveName: brokenASUS, DiscName: disc, DevicePath: path}
}

func lgAt(index int, path string, state int, disc string) makemkv.DriveInfo {
	return makemkv.DriveInfo{Index: index, State: state, DriveName: goodLG, DiscName: disc, DevicePath: path}
}

func eventsFor(events []DriveEvent, path string) []DriveEvent {
	var out []DriveEvent
	for _, ev := range events {
		if ev.DevicePath == path {
			out = append(out, ev)
		}
	}
	return out
}

func drivePaths(m *Manager) []string {
	var out []string
	for _, d := range m.GetAllDrives() {
		out = append(out, d.DevicePath())
	}
	return out
}

func TestIgnoredDriveIsNotTrackedAndRaisesNoEvents(t *testing.T) {
	exec := &mockExecutor{drives: []makemkv.DriveInfo{
		lgAt(0, "/dev/sr0", makemkv.DriveStateInserted, "MOVIE"),
		asusAt(1, "/dev/sr1", makemkv.DriveStateInserted, "OTHER"),
	}}
	var events []DriveEvent
	m := NewManager(exec, func(ev DriveEvent) { events = append(events, ev) })
	m.SetIgnoredDrives([]string{brokenASUS})

	m.PollOnce(context.Background())

	if got := drivePaths(m); len(got) != 1 || got[0] != "/dev/sr0" {
		t.Fatalf("drives = %v, want only /dev/sr0", got)
	}
	if m.GetDrive(1) != nil {
		t.Error("the ignored drive has a state machine")
	}
	if got := eventsFor(events, "/dev/sr1"); len(got) != 0 {
		t.Errorf("events for the ignored drive: %v", got)
	}
	if got := countType(events, EventDiscInserted); got != 1 {
		t.Errorf("disc_inserted events = %d, want 1 (the LG only)", got)
	}
}

// The drive index and device node both move when drives are replugged; the
// drive is recognised by its name.
func TestIgnoredDriveStaysIgnoredWhenRenumbered(t *testing.T) {
	exec := &scriptedExecutor{polls: [][]makemkv.DriveInfo{
		{lgAt(0, "/dev/sr0", makemkv.DriveStateEmptyClosed, ""), asusAt(1, "/dev/sr1", makemkv.DriveStateEmptyClosed, "")},
		{asusAt(0, "/dev/sr0", makemkv.DriveStateInserted, "DISC"), lgAt(1, "/dev/sr2", makemkv.DriveStateEmptyClosed, "")},
	}}
	var events []DriveEvent
	m := NewManager(exec, func(ev DriveEvent) { events = append(events, ev) })
	m.SetIgnoredDrives([]string{brokenASUS})

	m.PollOnce(context.Background())
	m.PollOnce(context.Background())

	for _, d := range m.GetAllDrives() {
		if d.DriveName() == brokenASUS {
			t.Fatalf("the ignored drive is tracked at index %d, %s", d.Index(), d.DevicePath())
		}
	}
	if got := countType(events, EventDiscInserted); got != 0 {
		t.Errorf("disc_inserted events = %d, want 0", got)
	}
}

func TestIgnoredDriveStaysIgnoredAfterFirmwareUpdate(t *testing.T) {
	flashed := asusAt(1, "/dev/sr1", makemkv.DriveStateInserted, "DISC")
	flashed.DriveName = "BD-RE ASUS BW-16D1HT 3.02 KLFO82D3028"
	exec := &mockExecutor{drives: []makemkv.DriveInfo{flashed}}
	m := NewManager(exec, nil)
	m.SetIgnoredDrives([]string{brokenASUS})

	m.PollOnce(context.Background())

	if len(m.GetAllDrives()) != 0 {
		t.Errorf("drives = %v, want none", drivePaths(m))
	}
}

// makemkvcon can momentarily report a real drive without its name. The device
// node does not move while the drive stays connected, so a nameless reading at
// the node the ignored drive held in the previous listing is still that drive.
func TestNamelessReadingOfIgnoredDriveIsStillIgnored(t *testing.T) {
	nameless := asusAt(1, "/dev/sr1", makemkv.DriveStateInserted, "DISC")
	nameless.DriveName = ""
	exec := &scriptedExecutor{polls: [][]makemkv.DriveInfo{
		{asusAt(1, "/dev/sr1", makemkv.DriveStateEmptyClosed, "")},
		{nameless},
	}}
	var events []DriveEvent
	m := NewManager(exec, func(ev DriveEvent) { events = append(events, ev) })
	m.SetIgnoredDrives([]string{brokenASUS})

	m.PollOnce(context.Background())
	m.PollOnce(context.Background())

	if len(m.GetAllDrives()) != 0 {
		t.Errorf("drives = %v, want none", drivePaths(m))
	}
	if got := eventsFor(events, "/dev/sr1"); len(got) != 0 {
		t.Errorf("events for the ignored drive: %v", got)
	}
}

// Changing the setting takes effect on the next poll, without a restart, and
// the drive leaves quietly: no eject or disconnect is announced for it.
func TestIgnoringATrackedDriveDropsItOnTheNextPoll(t *testing.T) {
	exec := &mockExecutor{drives: []makemkv.DriveInfo{
		lgAt(0, "/dev/sr0", makemkv.DriveStateEmptyClosed, ""),
		asusAt(1, "/dev/sr1", makemkv.DriveStateInserted, "DISC"),
	}}
	var events []DriveEvent
	m := NewManager(exec, func(ev DriveEvent) { events = append(events, ev) })
	m.PollOnce(context.Background())
	if m.GetDrive(1) == nil {
		t.Fatal("the drive should be tracked before it is ignored")
	}
	events = nil

	m.SetIgnoredDrives([]string{brokenASUS})
	m.PollOnce(context.Background())

	if m.GetDrive(1) != nil {
		t.Error("the drive is still tracked after it was ignored")
	}
	if len(events) != 0 {
		t.Errorf("events = %v, want none", events)
	}

	// Un-ignoring brings it back as an ordinary arrival.
	m.SetIgnoredDrives(nil)
	m.PollOnce(context.Background())
	if m.GetDrive(1) == nil {
		t.Fatal("the drive did not come back after it was un-ignored")
	}
	if got := countType(eventsFor(events, "/dev/sr1"), EventDiscInserted); got != 1 {
		t.Errorf("disc_inserted for the returning drive = %d, want 1", got)
	}
}

// The settings page lists every attached drive, ignored or not, so the user
// can un-ignore one.
func TestListedDrivesIncludesIgnoredDrives(t *testing.T) {
	exec := &mockExecutor{drives: []makemkv.DriveInfo{
		lgAt(0, "/dev/sr0", makemkv.DriveStateEmptyClosed, ""),
		asusAt(1, "/dev/sr1", makemkv.DriveStateEmptyClosed, ""),
		{Index: 2, State: makemkv.DriveStateNoDrive},
	}}
	m := NewManager(exec, nil)
	m.SetIgnoredDrives([]string{brokenASUS})
	m.PollOnce(context.Background())

	got := m.ListedDrives()
	if len(got) != 2 {
		t.Fatalf("listed drives = %v, want the LG and the ASUS", got)
	}
	names := map[string]bool{}
	for _, d := range got {
		names[d.DriveName] = true
	}
	if !names[brokenASUS] || !names[goodLG] {
		t.Errorf("listed drives = %v", got)
	}
}

// The physical watcher never polls an ignored drive's device node.
func TestWatchOnceDoesNotProbeIgnoredDrive(t *testing.T) {
	exec := &mockExecutor{drives: []makemkv.DriveInfo{
		lgAt(0, "/dev/sr0", makemkv.DriveStateEmptyClosed, ""),
		asusAt(1, "/dev/sr1", makemkv.DriveStateEmptyClosed, ""),
	}}
	m := NewManager(exec, nil)
	m.SetIgnoredDrives([]string{brokenASUS})
	m.PollOnce(context.Background())

	probe := newFakeProbe()
	probe.seq["/dev/sr0"] = []DriveStatus{StatusNoDisc}
	probe.seq["/dev/sr1"] = []DriveStatus{StatusNoDisc}
	m.probe = probe
	m.watchOnce(context.Background())

	if probe.i["/dev/sr1"] != 0 {
		t.Errorf("the ignored drive was probed %d times", probe.i["/dev/sr1"])
	}
	if probe.i["/dev/sr0"] != 1 {
		t.Errorf("the LG was probed %d times, want 1", probe.i["/dev/sr0"])
	}
}

// No volume label is read from an ignored drive, even if a disc arrival for
// its node was noticed just before the setting changed.
func TestNoteArrivalSkipsIgnoredDrive(t *testing.T) {
	exec := &mockExecutor{drives: []makemkv.DriveInfo{asusAt(1, "/dev/sr1", makemkv.DriveStateEmptyClosed, "")}}
	m := NewManager(exec, nil)
	labeler := &fakeLabeler{labels: map[string]string{"/dev/sr1": "DISC"}}
	m.labeler = labeler
	m.SetIgnoredDrives([]string{brokenASUS})
	m.PollOnce(context.Background())

	m.noteArrival(context.Background(), "/dev/sr1")
	// noteArrival reads the label on a goroutine; give one the chance to run.
	time.Sleep(50 * time.Millisecond)

	labeler.mu.Lock()
	defer labeler.mu.Unlock()
	if labeler.reads != 0 {
		t.Errorf("volume label read %d times from the ignored drive", labeler.reads)
	}
}
