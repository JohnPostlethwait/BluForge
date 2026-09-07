package drivemanager

import (
	"context"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// fakeProbe returns a scripted status sequence per device, sticking on the last.
type fakeProbe struct {
	seq map[string][]DriveStatus
	i   map[string]int
}

func newFakeProbe() *fakeProbe {
	return &fakeProbe{seq: map[string][]DriveStatus{}, i: map[string]int{}}
}

func (f *fakeProbe) Probe(path string) DriveStatus {
	s := f.seq[path]
	if len(s) == 0 {
		return StatusUnknown
	}
	idx := f.i[path]
	if idx >= len(s) {
		idx = len(s) - 1
	}
	f.i[path]++
	return s[idx]
}

// watchOnce probes every known drive and, on a confirmed physical removal,
// ejects the disc — even though nothing here ever touched the executor mutex.
func TestWatchOnceEjectsOnSustainedTrayOpen(t *testing.T) {
	var events []DriveEvent
	m := NewManager(&scriptedExecutor{polls: [][]makemkv.DriveInfo{withDisc("RAMBO")}}, func(ev DriveEvent) {
		events = append(events, ev)
	})
	m.PollOnce(context.Background())
	events = nil

	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }

	probe := newFakeProbe()
	probe.seq["/dev/sr1"] = []DriveStatus{StatusDiscOK, StatusTrayOpen, StatusTrayOpen}
	m.probe = probe

	m.watchOnce() // DiscOK baseline
	m.watchOnce() // TrayOpen pending
	if got := countType(events, EventDiscEjected); got != 0 {
		t.Fatalf("no eject expected before the confirm window, got %d", got)
	}
	now = now.Add(3 * time.Second)
	m.watchOnce() // TrayOpen confirmed -> eject

	if got := countType(events, EventDiscEjected); got != 1 {
		t.Fatalf("want 1 eject after sustained tray-open, got %d (%v)", got, events)
	}
	if dsm := m.GetDrive(1); dsm == nil || dsm.State() != StateEmpty {
		t.Fatalf("drive should be empty after physical eject")
	}
}

// A drive quietly reading a disc reports NotReady; the watcher must not eject.
func TestWatchOnceDoesNotEjectOnNotReady(t *testing.T) {
	var events []DriveEvent
	m := NewManager(&scriptedExecutor{polls: [][]makemkv.DriveInfo{withDisc("RAMBO")}}, func(ev DriveEvent) {
		events = append(events, ev)
	})
	m.PollOnce(context.Background())
	events = nil

	now := time.Unix(0, 0)
	m.now = func() time.Time { return now }

	probe := newFakeProbe()
	probe.seq["/dev/sr1"] = []DriveStatus{StatusDiscOK, StatusNotReady}
	m.probe = probe

	m.watchOnce()
	now = now.Add(30 * time.Second)
	m.watchOnce()
	m.watchOnce()

	if got := countType(events, EventDiscEjected); got != 0 {
		t.Fatalf("NotReady must never eject, got %d (%v)", got, events)
	}
}
