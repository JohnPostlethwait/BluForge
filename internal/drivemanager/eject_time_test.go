package drivemanager

import (
	"context"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// clockedManager drives PollOnce with a controllable clock.
type clockedManager struct {
	mgr    *Manager
	exec   *scriptedExecutor
	now    time.Time
	events *[]DriveEvent
}

func newClockedManager(t *testing.T, polls [][]makemkv.DriveInfo) *clockedManager {
	t.Helper()
	var events []DriveEvent
	cm := &clockedManager{
		exec:   &scriptedExecutor{polls: polls},
		now:    time.Date(2026, 8, 11, 20, 49, 0, 0, time.UTC),
		events: &events,
	}
	cm.mgr = NewManager(cm.exec, func(ev DriveEvent) { events = append(events, ev) })
	cm.mgr.now = func() time.Time { return cm.now }
	return cm
}

func (c *clockedManager) poll(advance time.Duration) {
	c.now = c.now.Add(advance)
	c.mgr.PollOnce(context.Background())
}

// Observed in production: a scan failed at 20:50:36 and disc_ejected fired at
// 20:50:38 — 1.7 seconds later — for a disc still in the drive. The poller
// blocks on the executor mutex for the length of a scan, so several polls then
// complete back to back while the drive is still settling. Counting consecutive
// polls is meaningless when their spacing collapses like that; only elapsed
// time is.
func TestBurstOfEmptyPollsDoesNotEject(t *testing.T) {
	cm := newClockedManager(t, [][]makemkv.DriveInfo{
		withDisc("POLICE STORY 2 4K UHD"),
		withoutDisc(),
		withoutDisc(),
		withoutDisc(),
		withoutDisc(),
	})

	cm.poll(0)                      // disc seen
	cm.poll(90 * time.Second)       // poller unblocks after a long scan
	cm.poll(300 * time.Millisecond) // ...and a burst of polls lands
	cm.poll(300 * time.Millisecond)
	cm.poll(300 * time.Millisecond)

	if n := countType(*cm.events, EventDiscEjected); n != 0 {
		t.Errorf("emitted %d ejects from a burst of empty polls spanning under a second", n)
	}
}

// A disc that is really gone still has to be reported, once the absence has
// lasted long enough to mean something.
func TestSustainedAbsenceOverTimeEjects(t *testing.T) {
	cm := newClockedManager(t, [][]makemkv.DriveInfo{
		withDisc("POLICE STORY 2 4K UHD"),
		withoutDisc(),
		withoutDisc(),
		withoutDisc(),
	})

	cm.poll(0)
	cm.poll(5 * time.Second)
	cm.poll(5 * time.Second)
	cm.poll(ejectConfirmDuration) // now the absence has persisted

	if n := countType(*cm.events, EventDiscEjected); n != 1 {
		t.Errorf("emitted %d ejects for a genuinely removed disc, want 1", n)
	}
}

// Seeing the disc again resets the clock, so an intermittent reading never
// accumulates toward an eject.
func TestIntermittentAbsenceNeverEjects(t *testing.T) {
	cm := newClockedManager(t, [][]makemkv.DriveInfo{
		withDisc("POLICE STORY 2 4K UHD"),
		withoutDisc(),
		withDisc("POLICE STORY 2 4K UHD"),
		withoutDisc(),
		withDisc("POLICE STORY 2 4K UHD"),
		withoutDisc(),
	})

	for i := 0; i < 6; i++ {
		cm.poll(60 * time.Second)
	}

	if n := countType(*cm.events, EventDiscEjected); n != 0 {
		t.Errorf("emitted %d ejects for a disc that kept reappearing", n)
	}
}
