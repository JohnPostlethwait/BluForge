package templates

import (
	"strings"
	"testing"
)

// A disc swap produces an insert, not an eject: an eject is only believed after
// the drive reports empty for a continuous 30 seconds, which taking one disc
// out and putting the next in never reaches. The page reset everything it knew
// on eject and nothing at all on insert, so ripping one film and loading the
// next left the new disc's page showing the previous film's match and titles.
func TestDriveDetailResetsOnDiscInsert(t *testing.T) {
	html := renderDriveDetail(t)

	if !strings.Contains(html, "'disc_inserted'") {
		t.Error("the drive page does not react to disc_inserted at all; " +
			"a swap leaves the previous disc's match on screen")
	}
}

// Both events mean the disc this page describes is gone, so both have to clear
// the same state. Sharing one reset is what stops them drifting apart — which
// is how insert came to clear nothing while eject cleared twelve fields.
func TestDriveDetailUsesOneResetForBothDiscEvents(t *testing.T) {
	html := renderDriveDetail(t)

	handler := strings.Index(html, "evtSource.on('drive-event'")
	if handler < 0 {
		t.Fatal("no drive-event handler on the drive page")
	}
	body := html[handler:]
	if end := strings.Index(body, "evtSource.on('disc_recovery'"); end > 0 {
		body = body[:end]
	}

	if !strings.Contains(body, "'disc_ejected'") || !strings.Contains(body, "'disc_inserted'") {
		t.Error("the drive-event handler does not act on both disc_ejected and disc_inserted")
	}
	if !strings.Contains(body, "resetForNewDisc(") {
		t.Error("the drive-event handler does not run the shared reset")
	}
	if !strings.Contains(html, "function resetForNewDisc(") {
		t.Error("resetForNewDisc is not defined")
	}
}

// The reset is only worth having if it clears the state that actually carried
// over: the match, the search behind it, and the titles named from it.
func TestDriveDetailResetClearsTheDiscBoundState(t *testing.T) {
	html := renderDriveDetail(t)

	fn := strings.Index(html, "function resetForNewDisc(")
	if fn < 0 {
		t.Fatal("resetForNewDisc is not defined")
	}
	body := html[fn:]
	if end := strings.Index(body, "\n}"); end > 0 {
		body = body[:end]
	}

	for _, cleared := range []string{
		"store.titles = []",
		"store.selectedRelease = null",
		"store.searchResults = []",
		"store.hasMapping = false",
		"store.currentStep = 1",
	} {
		if !strings.Contains(body, cleared) {
			t.Errorf("resetForNewDisc does not do %q", cleared)
		}
	}
}

// The stale-state problem the drive page solved with resyncDriveState applies
// just as much to the other two live pages: events are delivered once, never
// replayed, and a dropped connection or a sleeping laptop loses them.
func TestDashboardResyncsWhenTheEventStreamReturns(t *testing.T) {
	html := renderDashboard(t)

	if !strings.Contains(html, "resyncDrives") {
		t.Fatal("the dashboard has no resync function")
	}
	// openEvents (static/events.js) resyncs on reconnect, on a stalled stream,
	// and when the tab comes back to the front.
	if !strings.Contains(html, "openEvents(resyncDrives)") {
		t.Error("the dashboard does not open its stream through openEvents with its resync")
	}
	if strings.Contains(html, "new EventSource(") {
		t.Error("the dashboard opens a raw EventSource, which nothing resyncs")
	}
}

func TestActivityResyncsWhenTheEventStreamReturns(t *testing.T) {
	html := renderActivity(t)

	if !strings.Contains(html, "resyncActivity") {
		t.Fatal("the activity page has no resync function")
	}
	if !strings.Contains(html, "openEvents(resyncActivity)") {
		t.Error("the activity page does not open its stream through openEvents with its resync")
	}
	if strings.Contains(html, "new EventSource(") {
		t.Error("the activity page opens a raw EventSource, which nothing resyncs")
	}
}

// A salvage that ended while this page was disconnected takes its terminal
// event with it: events are delivered once and never replayed. Restoring a
// running salvage on resync is only half the job — a resync that finds nothing
// running has to take the spinner down, or the page shows "Copying the disc"
// for a salvage that finished hours ago.
func TestActivityResyncClearsASalvageThatEndedWhileAway(t *testing.T) {
	html := renderActivity(t)

	fn := strings.Index(html, "function applySalvageState(")
	if fn < 0 {
		t.Fatal("applySalvageState is not defined")
	}
	body := html[fn:]
	if end := strings.Index(body, "\n}"); end > 0 {
		body = body[:end]
	}

	if !strings.Contains(body, "store.salvageActive") {
		t.Fatal("applySalvageState never touches salvageActive")
	}
	// The active and paused branches both set it; a third branch is what handles
	// the server reporting neither.
	if strings.Count(body, "store.salvageActive = false") < 2 {
		t.Error("applySalvageState has no branch for the server reporting no salvage at all; " +
			"a stale spinner would never come down")
	}
}

// A rip submitted from the drive page while the activity tab is open produced a
// rip-update for a job the page had never heard of. The handler only ever
// updated a job already in store.active, so the whole rip ran and finished
// without ever appearing.
func TestActivityAdoptsAJobItHasNotSeenBefore(t *testing.T) {
	html := renderActivity(t)

	handler := strings.Index(html, "evtSource.on('rip-update'")
	if handler < 0 {
		t.Fatal("no rip-update handler on the activity page")
	}
	if !strings.Contains(html, "resyncActivity") {
		t.Error("the rip-update handler has no way to pick up an unknown job")
	}
}

// A scan's "done" lost with the event stream left the banner up for six
// minutes and never loaded the titles: the resync only took the banner down.
// It has to end the scan exactly as the missed event would have.
func TestDriveResyncFinishesAScanWhoseEventWasMissed(t *testing.T) {
	html := renderDriveDetail(t)

	if !strings.Contains(html, "openEvents(resyncDriveState)") {
		t.Error("the drive page does not open its stream through openEvents with its resync")
	}

	fn := strings.Index(html, "function resyncDriveState(")
	if fn < 0 {
		t.Fatal("resyncDriveState is not defined")
	}
	body := html[fn:]
	if end := strings.Index(body, "\n}"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "s.lastScanPhase") || !strings.Contains(body, "finishScan(") {
		t.Error("resyncDriveState does not finish a scan from the server's last outcome")
	}

	finish := strings.Index(html, "function finishScan(")
	if finish < 0 {
		t.Fatal("finishScan is not defined")
	}
	fbody := html[finish:]
	if end := strings.Index(fbody, "\n}"); end > 0 {
		fbody = fbody[:end]
	}
	if !strings.Contains(fbody, "loadScanResults(") {
		t.Error("finishScan does not load the titles of a finished scan")
	}
}
