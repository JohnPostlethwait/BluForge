package drivemanager

import (
	"testing"
	"time"
)

// The watcher turns a stream of raw drive-status readings into media
// present/gone decisions, with a short confirm window so a momentary blip does
// not count. These tests drive observe() directly with a controllable clock —
// no goroutine, no hardware.

func TestWatcherReportsMediaGoneAfterSustainedTrayOpen(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	w := newOpticalWatcher(clock, 2*time.Second)

	if got := w.observe("/dev/sr0", StatusDiscOK); got != MediaNoChange {
		t.Fatalf("baseline disc present should be no change, got %v", got)
	}
	// Tray just opened — not believed until it persists.
	if got := w.observe("/dev/sr0", StatusTrayOpen); got != MediaNoChange {
		t.Fatalf("first tray-open reading should not confirm yet, got %v", got)
	}
	// Absence has now lasted past the confirm window.
	now = now.Add(3 * time.Second)
	if got := w.observe("/dev/sr0", StatusTrayOpen); got != MediaGone {
		t.Fatalf("sustained tray-open should report MediaGone, got %v", got)
	}
}

func TestWatcherIgnoresBriefTrayOpenBlip(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	w := newOpticalWatcher(clock, 2*time.Second)

	w.observe("/dev/sr0", StatusDiscOK)
	// A single stray reading, then the disc is back before the window elapses.
	if got := w.observe("/dev/sr0", StatusTrayOpen); got != MediaNoChange {
		t.Fatalf("stray reading should not confirm, got %v", got)
	}
	now = now.Add(500 * time.Millisecond)
	if got := w.observe("/dev/sr0", StatusDiscOK); got != MediaNoChange {
		t.Fatalf("recovered disc should cancel the pending gone, got %v", got)
	}
	// Even long after, no gone should fire from that blip.
	now = now.Add(10 * time.Second)
	if got := w.observe("/dev/sr0", StatusDiscOK); got != MediaNoChange {
		t.Fatalf("no gone should fire once the blip recovered, got %v", got)
	}
}

// A busy drive mid-scan reports DRIVE_NOT_READY. That is not an eject, and must
// never fire a gone that would cancel the running scan. This is the exact
// false-positive class that made past releases cancel real work.
func TestWatcherTreatsNotReadyAsNoInformation(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	w := newOpticalWatcher(clock, 2*time.Second)

	w.observe("/dev/sr0", StatusDiscOK)
	w.observe("/dev/sr0", StatusNotReady)
	now = now.Add(30 * time.Second)
	if got := w.observe("/dev/sr0", StatusNotReady); got != MediaNoChange {
		t.Fatalf("NotReady must never confirm a gone, got %v", got)
	}
	// And the disc coming back to OK is still no change — it never left.
	if got := w.observe("/dev/sr0", StatusDiscOK); got != MediaNoChange {
		t.Fatalf("disc that never left should be no change, got %v", got)
	}
}

func TestWatcherReportsMediaPresentAfterInsert(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	w := newOpticalWatcher(clock, 2*time.Second)

	// Baseline: empty.
	w.observe("/dev/sr0", StatusNoDisc)
	// Disc goes in.
	if got := w.observe("/dev/sr0", StatusDiscOK); got != MediaNoChange {
		t.Fatalf("first present reading should not confirm yet, got %v", got)
	}
	now = now.Add(3 * time.Second)
	if got := w.observe("/dev/sr0", StatusDiscOK); got != MediaPresent {
		t.Fatalf("sustained disc present should report MediaPresent, got %v", got)
	}
}

// A vanished device node (ENODEV) is a disconnect, and it means the media is
// gone too — a running scan must be cancelled just as for an eject.
func TestWatcherReportsGoneWhenDeviceVanishes(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	w := newOpticalWatcher(clock, 2*time.Second)

	w.observe("/dev/sr0", StatusDiscOK)
	w.observe("/dev/sr0", StatusGone)
	now = now.Add(3 * time.Second)
	if got := w.observe("/dev/sr0", StatusGone); got != MediaGone {
		t.Fatalf("vanished device should report MediaGone, got %v", got)
	}
}

func TestWatcherTracksDrivesIndependently(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	w := newOpticalWatcher(clock, 2*time.Second)

	w.observe("/dev/sr0", StatusDiscOK)
	w.observe("/dev/sr1", StatusDiscOK)

	// sr0 ejects; sr1 stays put.
	w.observe("/dev/sr0", StatusTrayOpen)
	now = now.Add(3 * time.Second)
	if got := w.observe("/dev/sr0", StatusTrayOpen); got != MediaGone {
		t.Fatalf("sr0 should report gone, got %v", got)
	}
	if got := w.observe("/dev/sr1", StatusDiscOK); got != MediaNoChange {
		t.Fatalf("sr1 was untouched and must not report a change, got %v", got)
	}
}
