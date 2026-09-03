package drivemanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// absentSlot is how makemkvcon reports a slot with no hardware in it: state
// 256, and every string field blank.
//
// A wedged drive reports exactly the same thing. That is the whole difficulty —
// the listing cannot tell "unplugged" from "attached but not answering".
func absentSlot() []makemkv.DriveInfo {
	return []makemkv.DriveInfo{{Index: 1, State: makemkv.DriveStateNoDrive, Flags: 0}}
}

// Observed in production. A rip finished at 04:25:45 and the drive was polled
// happily every five seconds, each listing taking four to five seconds. At
// 04:30:27 the listings started timing out; at 04:32:48 one came back after
// twenty-four seconds — five times its usual — reporting the drive as an empty
// slot, and every listing after it timed out again.
//
// That single degraded reading removed the drive outright. A disc reporting
// absent is disbelieved for thirty seconds precisely because one reading proves
// nothing; a drive reporting absent was believed instantly, and because the
// drive is deleted from the map rather than merely reset, there was nothing
// left to correct when it came back.
func TestOneAbsentListingDoesNotRemoveTheDrive(t *testing.T) {
	cm := newClockedManager(t, [][]makemkv.DriveInfo{
		withDisc("SKY_CAPTAIN"),
		absentSlot(),
		withDisc("SKY_CAPTAIN"),
	})

	cm.poll(0)
	cm.poll(24 * time.Second) // the degraded listing
	cm.poll(5 * time.Second)  // makemkvcon can see the drive again

	if n := countType(*cm.events, EventDriveDisconnect); n != 0 {
		t.Errorf("emitted %d disconnects for a drive that was missing from one listing", n)
	}
	if got := len(cm.mgr.GetAllDrives()); got != 1 {
		t.Errorf("GetAllDrives returned %d drives, want 1 — the drive was never unplugged", got)
	}
}

// The absence has to persist in listings that actually happened. In the
// production trace every poll after the degraded one was killed at the thirty
// second timeout, so nothing ever corroborated it — and the card for a drive
// that is still plugged in must stay.
func TestAbsenceIsNotConfirmedByFailedPolls(t *testing.T) {
	exec := &failingExecutor{drives: withDisc("SKY_CAPTAIN")}
	var events []DriveEvent
	mgr := NewManager(exec, func(ev DriveEvent) { events = append(events, ev) })
	now := time.Date(2026, 9, 1, 4, 25, 45, 0, time.UTC)
	mgr.now = func() time.Time { return now }

	mgr.PollOnce(context.Background())

	// One listing struggles back with the drive missing.
	now = now.Add(24 * time.Second)
	exec.drives = absentSlot()
	mgr.PollOnce(context.Background())

	// Then makemkvcon stops answering for well past the debounce.
	exec.err = errors.New("makemkv: list drives: signal: killed")
	for range 10 {
		now = now.Add(35 * time.Second)
		mgr.PollOnce(context.Background())
	}

	if n := countType(events, EventDriveDisconnect); n != 0 {
		t.Errorf("emitted %d disconnects on the strength of listings that failed", n)
	}
	if got := len(mgr.GetAllDrives()); got != 1 {
		t.Errorf("GetAllDrives returned %d drives, want 1", got)
	}
}

// A drive that really was unplugged still has to go, once listings that can see
// the rest of the machine have agreed on it for long enough.
func TestSustainedDriveAbsenceDisconnectsOnce(t *testing.T) {
	cm := newClockedManager(t, [][]makemkv.DriveInfo{
		withDisc("SKY_CAPTAIN"),
		absentSlot(),
		absentSlot(),
		absentSlot(),
		absentSlot(),
	})

	cm.poll(0)
	cm.poll(5 * time.Second)
	cm.poll(5 * time.Second)
	cm.poll(driveGoneConfirmDuration)
	cm.poll(5 * time.Second)

	if n := countType(*cm.events, EventDriveDisconnect); n != 1 {
		t.Errorf("emitted %d disconnects for a genuinely unplugged drive, want 1", n)
	}
	if got := len(cm.mgr.GetAllDrives()); got != 0 {
		t.Errorf("GetAllDrives returned %d drives after the only one was unplugged, want 0", got)
	}
}

// A drive that flickers out and back must not carry its half-elapsed absence
// into the next one, or two unrelated blips thirty seconds apart would remove a
// drive that was present in between.
func TestAReturningDriveResetsItsAbsence(t *testing.T) {
	cm := newClockedManager(t, [][]makemkv.DriveInfo{
		withDisc("SKY_CAPTAIN"),
		absentSlot(),
		withDisc("SKY_CAPTAIN"),
		absentSlot(),
	})

	cm.poll(0)
	cm.poll(20 * time.Second)
	cm.poll(5 * time.Second)  // back
	cm.poll(20 * time.Second) // gone again, but the clock starts over

	if n := countType(*cm.events, EventDriveDisconnect); n != 0 {
		t.Errorf("emitted %d disconnects across two short absences with the drive present between them", n)
	}
}

// failingExecutor serves a drive listing until err is set, after which every
// poll fails the way a killed makemkvcon does.
type failingExecutor struct {
	drives []makemkv.DriveInfo
	err    error
}

func (f *failingExecutor) ListDrives(context.Context) ([]makemkv.DriveInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.drives, nil
}

func (f *failingExecutor) ScanDisc(context.Context, int) (*makemkv.DiscScan, error) {
	return nil, nil
}
