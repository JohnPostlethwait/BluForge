package drivemanager

import (
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// Perfect Blue ships as a two-disc set whose main disc and bonus disc report
// the same volume label. Swapping one for the other emits no event at all: a
// swap takes a few seconds and the eject debounce waits 30, so every empty poll
// in between is swallowed, and the disc that comes back reports the name the
// old one had.
//
// This test pins that as the boundary, not as a defect to be fixed here. The
// poller's only evidence is the name on a DRV line, and there is no name that
// distinguishes these two discs. Making an empty reading count sooner would
// reinstate the spurious eject that TestTransientEmptyPollDoesNotEject exists
// to prevent — it wiped a release selection mid-backup in production.
//
// The swap is caught where the evidence is: workflow.Orchestrator fingerprints
// what a scan actually found, and a rescan that comes back describing a
// different disc replaces the cache and reports the change. See
// TestRescanReadsTheDiscRatherThanTheCache.
func TestSameLabelDiscSwapIsInvisibleToThePoller(t *testing.T) {
	events := collectEvents(t, [][]makemkv.DriveInfo{
		withDisc("PERFECT_BLUE"), // main disc
		withoutDisc(),            // tray open, swapping
		withDisc("PERFECT_BLUE"), // bonus disc, same volume label
		withDisc("PERFECT_BLUE"),
	})

	if n := countType(events, EventDiscInserted); n != 1 {
		t.Errorf("emitted %d insert events, want 1 — the poller cannot tell these discs apart, "+
			"and pretending otherwise costs a spurious eject", n)
	}
	if n := countType(events, EventDiscEjected); n != 0 {
		t.Errorf("emitted %d eject events during a swap shorter than the debounce, want 0", n)
	}
}
