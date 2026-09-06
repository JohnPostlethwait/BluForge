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
