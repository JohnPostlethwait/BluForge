package templates

import (
	"strings"
	"testing"
)

// ripUpdateBody returns the dashboard's rip-update handler.
func ripUpdateBody(t *testing.T) string {
	t.Helper()
	html := renderDashboard(t)
	start := strings.Index(html, "addEventListener('rip-update'")
	if start < 0 {
		t.Fatal("no rip-update handler on the dashboard")
	}
	body := html[start:]
	if end := strings.Index(body, "addEventListener('contribution_available'"); end > 0 {
		body = body[:end]
	}
	return body
}

// The dashboard kept its own running totals by arithmetic on events, and the
// arithmetic was wrong in two ways that only drift further apart the longer the
// page stays open:
//
//   - completedToday was incremented for a failed rip as readily as a completed
//     one, so the "Today" figure counted failures as successes.
//   - queuedCount was decremented on every terminal event, whether or not the
//     job had ever been queued. A single-title rip, which never queues at all,
//     still took one off the count.
//
// Neither figure needs deriving. The server computes all three from the engine
// and the database, and since the dashboard can now ask it, guessing is the
// only thing producing wrong numbers.
func TestDashboardDoesNotGuessItsCounters(t *testing.T) {
	body := ripUpdateBody(t)

	if strings.Contains(body, "completedToday++") {
		t.Error("the dashboard still increments completedToday itself; " +
			"a failed rip is counted as one completed today")
	}
	if strings.Contains(body, "queuedCount--") {
		t.Error("the dashboard still decrements queuedCount itself; " +
			"a rip that was never queued still takes one off")
	}
	if !strings.Contains(body, "resyncDrives()") {
		t.Error("a finished rip does not ask the server for the real counts")
	}
}

// The card itself should still respond immediately rather than waiting on a
// round trip — the counters are what come from the server, not the progress.
func TestDashboardStillUpdatesTheCardImmediately(t *testing.T) {
	body := ripUpdateBody(t)

	if !strings.Contains(body, "drive.ripProgress") {
		t.Error("the rip-update handler no longer touches the drive's progress")
	}
}
