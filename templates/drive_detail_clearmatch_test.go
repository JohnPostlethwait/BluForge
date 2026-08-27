package templates

import (
	"strings"
	"testing"
)

// A wrong match is only discoverable by eye, on the steps that display it. The
// way out has to be on each of them: a page reload lands on Review Titles, so
// an affordance only on the search step is unreachable in practice.
func TestClearMatchIsOfferedWhereTheMatchIsShown(t *testing.T) {
	html := renderDriveDetail(t)

	if got := strings.Count(html, "clearMatch(1)"); got < 3 {
		t.Errorf("clearMatch appears %d times, want at least 3 — the saved-match banner, "+
			"the Scan Disc match confirmation, and the Review Titles header", got)
	}
}

// The button has to be wired to the drive it is rendered for.
func TestClearMatchIsWiredToTheRenderedDrive(t *testing.T) {
	html := renderDriveDetail(t)

	if !strings.Contains(html, "clearMatch(1)") {
		t.Error("clearMatch is not called with the rendered drive index")
	}
	if strings.Contains(html, "clearMatch(0)") {
		t.Error("clearMatch is hardcoded to drive 0")
	}
}

// "Clear & Re-scan" was an <a href> GET aimed at a POST-only route, so every
// click returned 405 and the only escape from a saved match did nothing. The
// route has since been deleted outright, which makes the link a 404 instead —
// still broken, and still the thing to guard against reintroducing.
func TestClearAndRescanIsNotADeadGetLink(t *testing.T) {
	html := renderDriveDetail(t)

	if strings.Contains(html, `href="/drives/1/rescan"`) {
		t.Error("the saved-match banner links to /rescan, a route that no longer exists")
	}
}

// Clearing throws away a saved mapping and the scanned titles, so it must not
// happen on a stray click.
func TestClearMatchConfirmsBeforeDiscarding(t *testing.T) {
	html := renderDriveDetail(t)

	fn := strings.Index(html, "async function clearMatch")
	if fn < 0 {
		t.Fatal("clearMatch is not defined on the page")
	}
	body := html[fn:]
	if end := strings.Index(body, "\nasync function scanDisc"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "confirm(") {
		t.Error("clearMatch discards a saved match and a scan without asking first")
	}
}

// resyncDriveState deliberately refuses to blank an existing title list, so
// clearing must empty the store itself or the discarded match's titles stay on
// screen.
func TestClearMatchEmptiesTheTitlesLocally(t *testing.T) {
	html := renderDriveDetail(t)

	fn := strings.Index(html, "async function clearMatch")
	if fn < 0 {
		t.Fatal("clearMatch is not defined on the page")
	}
	body := html[fn:]
	if end := strings.Index(body, "\nasync function scanDisc"); end > 0 {
		body = body[:end]
	}

	for _, reset := range []string{
		"store.titles = []",
		"store.selectedRelease = null",
		"store.searchResults = []",
		"store.hasMapping = false",
		"store.scanCachedAt = 0",
		"store.currentStep = 1",
	} {
		if !strings.Contains(body, reset) {
			t.Errorf("clearMatch does not reset %q; the discarded match survives on screen", reset)
		}
	}
}
