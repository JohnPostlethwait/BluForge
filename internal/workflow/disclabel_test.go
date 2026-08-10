package workflow

import (
	"context"
	"testing"
)

// labelledScanner reports a disc label the way the drive poller does, via the
// DRV line, while its scan produces none.
type labelledScanner struct {
	failingScanner
	label string
}

func (l *labelledScanner) DiscLabelForDrive(_ context.Context, _ int) string {
	return l.label
}

// Observed on a real disc: the failed scan carried no CINFO disc name, so the
// diagnostics row and the scratch directory were both named after nothing. The
// label is known — the drive listing has it — and it is the field the whole
// per-disc record is keyed on.
func TestDiscLabelFallsBackToTheDriveListing(t *testing.T) {
	scanner := &labelledScanner{
		failingScanner: failingScanner{devicePath: "/dev/sr1"},
		label:          "STARGATE_SG1_S4_D3",
	}

	got := discLabelFor(context.Background(), scanner, 1, "")
	if got != "STARGATE_SG1_S4_D3" {
		t.Errorf("label = %q, want the drive's disc name", got)
	}
}

// A label from the scan itself is authoritative and must win.
func TestDiscLabelPrefersTheScan(t *testing.T) {
	scanner := &labelledScanner{
		failingScanner: failingScanner{devicePath: "/dev/sr1"},
		label:          "FROM_DRIVE",
	}

	if got := discLabelFor(context.Background(), scanner, 1, "FROM_SCAN"); got != "FROM_SCAN" {
		t.Errorf("label = %q, want FROM_SCAN", got)
	}
}

// A scanner that cannot report a label is not an error; the record just carries
// what is known.
func TestDiscLabelWithoutAnySource(t *testing.T) {
	if got := discLabelFor(context.Background(), &failingScanner{}, 1, ""); got != "" {
		t.Errorf("label = %q, want empty", got)
	}
}

// The scratch directory is named from the label, so an unnamed disc must still
// produce a stable, distinct directory rather than colliding with every other
// unnamed disc.
func TestScratchSlugStableWithoutLabel(t *testing.T) {
	a := scratchSlug("", "/dev/sr0")
	b := scratchSlug("", "/dev/sr1")

	if a == b {
		t.Errorf("unnamed discs in different drives share a scratch dir: %q", a)
	}
	if a != scratchSlug("", "/dev/sr0") {
		t.Error("scratch slug is not stable across calls")
	}
}
