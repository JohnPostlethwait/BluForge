package makemkv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDebug(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "debug.log")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

// makemkvcon writes the reason for a fatal exit to its debug log, not the robot
// stream. On a failure we show the tail of that log — the last lines, where the
// error is — so the failure finally says why.
func TestTailLinesReturnsTheLastLines(t *testing.T) {
	p := writeDebug(t, "a", "b", "c", "d", "e")

	got := tailLines(p, 3)

	want := []string{"c", "d", "e"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A short log is returned whole, not padded or truncated.
func TestTailLinesReturnsAShortLogWhole(t *testing.T) {
	p := writeDebug(t, "only", "two")
	if got := tailLines(p, 40); len(got) != 2 {
		t.Errorf("got %d lines, want 2: %v", len(got), got)
	}
}

// Blank lines are noise; the tail should carry content.
func TestTailLinesDropsBlankLines(t *testing.T) {
	p := writeDebug(t, "real", "", "   ", "also real")
	got := tailLines(p, 40)
	for _, l := range got {
		if strings.TrimSpace(l) == "" {
			t.Errorf("a blank line survived: %q in %v", l, got)
		}
	}
	if len(got) != 2 {
		t.Errorf("got %d lines, want 2: %v", len(got), got)
	}
}

// A missing debug file (makemkvcon never wrote one) is not an error — it just
// yields nothing to show.
func TestTailLinesOnMissingFileIsEmpty(t *testing.T) {
	if got := tailLines(filepath.Join(t.TempDir(), "nope.log"), 40); got != nil {
		t.Errorf("missing file returned %v, want nil", got)
	}
}

// makemkvcon ignores the path we pass to --debug and writes its log where it
// pleases — but it announces where, so we take the path from its own words
// rather than guessing.
func TestParseDebugLogPathReadsTheAnnouncedPath(t *testing.T) {
	line := "Debug logging enabled, log will be saved as file:///tmp/bluforge-makemkv-home-123/MakeMKV_log.txt"

	got, ok := parseDebugLogPath(line)
	if !ok {
		t.Fatal("did not recognise the announce line")
	}
	if got != "/tmp/bluforge-makemkv-home-123/MakeMKV_log.txt" {
		t.Errorf("path = %q, want the file path with file:// stripped", got)
	}
}

func TestParseDebugLogPathIgnoresOtherLines(t *testing.T) {
	for _, line := range []string{
		"Using LibreDrive mode (v06.3)",
		"DEBUG: Code 0 at `BLLaJX7%0",
		"File 00001.mpls was added as title #5",
	} {
		if _, ok := parseDebugLogPath(line); ok {
			t.Errorf("wrongly parsed a path out of %q", line)
		}
	}
}

// The obfuscated DEBUG markers and the announce line are makemkvcon noise, not
// something to show the user — the real reason is in the log file. They are
// dropped from the failure capture.
func TestIsDebugNoiseCatchesTheObfuscatedMarkersAndAnnounce(t *testing.T) {
	noise := []string{
		"DEBUG: Code 0 at `BLLaJX7%0; ?J)zOB:`KC:29393631",
		"Debug logging enabled, log will be saved as file:///tmp/x/MakeMKV_log.txt",
	}
	for _, l := range noise {
		if !isDebugNoise(l) {
			t.Errorf("did not treat as noise: %q", l)
		}
	}
	keep := []string{
		"File 00001.mpls was added as title #5",
		"Error 'Scsi error' occurred while reading",
	}
	for _, l := range keep {
		if isDebugNoise(l) {
			t.Errorf("wrongly treated as noise: %q", l)
		}
	}
}
