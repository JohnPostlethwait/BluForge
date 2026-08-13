package web

import "testing"

// The offer appears only for the failure a salvage can actually get past: a rip
// that read the disc and came away with nothing, which is what a scratch does.
func TestSalvageIsOfferedForAReadFailure(t *testing.T) {
	for _, msg := range []string{
		"makemkv: rip disc:0 title 0: makemkvcon saved no titles — the drive could not read it",
		"no .mkv file found in /output/.rip-770957343/t0-1303664353",
	} {
		if !salvageable("failed", msg) {
			t.Errorf("no salvage offered for %q", msg)
		}
	}
}

// Putting a two-hour operation in front of someone whose disk is full would be
// worse than saying nothing.
func TestSalvageIsNotOfferedForOtherFailures(t *testing.T) {
	for _, msg := range []string{
		"insufficient disk space: need 64 GB, have 12 GB",
		"organize: rename /output/tmp: permission denied",
		"makemkv: title 00000.mpls moved from index 4 to 3 in this pass",
	} {
		if salvageable("failed", msg) {
			t.Errorf("salvage offered for %q, which it cannot help", msg)
		}
	}
}

// A rip that worked has nothing to salvage.
func TestSalvageIsNotOfferedForASuccess(t *testing.T) {
	if salvageable("completed", "") {
		t.Error("salvage offered for a completed rip")
	}
	if salvageable("ripping", "makemkvcon saved no titles") {
		t.Error("salvage offered for a rip still running")
	}
}
