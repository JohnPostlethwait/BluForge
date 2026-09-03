package ripper

import (
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// A failed job leaves the engine's active map before it settles, so the
// activity page only ever sees it again through the database — and the capture
// is deliberately not written there. Without somewhere to keep it, what MakeMKV
// said would reach the log and never the page.
func TestTheEngineKeepsARecentFailureForThePage(t *testing.T) {
	e := NewEngine(&failingRipExecutor{})
	e.recordFailure(42, []makemkv.ScanWarning{{Code: 3308, Text: "File 00200.mpls (angle 1)", Count: 1}}, 0)

	got, dropped, ok := e.RecentFailure(42)
	if !ok {
		t.Fatal("the failure was not kept")
	}
	if len(got) != 1 || got[0].Code != 3308 {
		t.Errorf("kept %+v, want the captured message", got)
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
}

func TestAJobWithNoRecordedFailureReportsNone(t *testing.T) {
	e := NewEngine(&failingRipExecutor{})
	if _, _, ok := e.RecentFailure(7); ok {
		t.Error("a job that never failed reported a capture")
	}
}

// The map is memory, and BluForge runs for weeks. Without a bound, a library
// that fails often would hold every failure it ever had.
func TestRecentFailuresAreBounded(t *testing.T) {
	e := NewEngine(&failingRipExecutor{})
	for id := range int64(recentFailureLimit + 10) {
		e.recordFailure(id, []makemkv.ScanWarning{{Text: "x", Count: 1}}, 0)
	}

	if got := e.recentFailureCount(); got > recentFailureLimit {
		t.Errorf("kept %d failures, want at most %d", got, recentFailureLimit)
	}
	// The newest must survive: a user looks at the failure that just happened.
	if _, _, ok := e.RecentFailure(int64(recentFailureLimit + 9)); !ok {
		t.Error("the most recent failure was evicted")
	}
}
