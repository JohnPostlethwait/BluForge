package templates

import (
	"context"
	"strings"
	"testing"
)

func renderActivity(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	if err := Activity(ActivityPageData{StoreJSON: "{}"}).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render activity: %v", err)
	}
	return sb.String()
}

// The drive page only knows about the disc in the drive right now. A copy is
// most often noticed from the finished rip it produced, which lives in history,
// so the offer to reclaim the space has to be reachable from there too.
func TestHistoryOffersToDiscardTheRepairedCopy(t *testing.T) {
	html := renderActivity(t)

	if !strings.Contains(html, "discardDiscBackup(r.discName)") {
		t.Error("no discard control on the history entries")
	}
	if !strings.Contains(html, "discHasBackup(r.discName)") {
		t.Error("the discard control is not gated on the copy still existing")
	}
}

// Keyed by disc, not by drive: history outlives the drive numbering, and a
// stale index would delete a different disc's copy.
func TestDiscardFromHistoryIsKeyedByDisc(t *testing.T) {
	html := renderActivity(t)

	i := strings.Index(html, "async function discardDiscBackup")
	if i < 0 {
		t.Fatal("discardDiscBackup is not defined")
	}
	body := html[i:]
	if end := strings.Index(body, "async function salvageDisc"); end > 0 {
		body = body[:end]
	}

	if !strings.Contains(body, "disc: discName") {
		t.Error("the request does not identify the copy by disc name")
	}
	if strings.Contains(body, "driveIndex") {
		t.Error("the request depends on a drive index, which history cannot trust")
	}
}
