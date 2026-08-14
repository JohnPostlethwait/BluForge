package templates

import (
	"context"
	"strings"
	"testing"
)

func renderDriveDetail(t *testing.T) string {
	t.Helper()
	var sb strings.Builder
	if err := DriveDetail(DriveDetailData{DriveIndex: 1, DiscName: "RAMBO_DISC2"}).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render drive detail: %v", err)
	}
	return sb.String()
}

// The repaired-copy notice explains why scanning is instant and why the drive
// never spins. Nested inside a wizard step it only appeared once the user
// reached that step — after the scan it was there to explain — so it must sit
// outside every step gate.
func TestRepairedCopyNoticeIsNotHiddenBehindAStep(t *testing.T) {
	html := renderDriveDetail(t)

	notice := strings.Index(html, "Reading a repaired copy of this disc")
	if notice < 0 {
		t.Fatal("the repaired-copy notice is not on the page at all")
	}

	// Every step gate follows step 1's. Appearing before it means the notice
	// belongs to the page rather than to any one step.
	firstStep := strings.Index(html, "currentStep === 1")
	if firstStep < 0 {
		t.Fatal("no step gate found — this test no longer measures anything")
	}
	if notice > firstStep {
		t.Error("the repaired-copy notice sits inside a wizard step, so it is invisible until the user reaches that step")
	}
}

// A notice with no way to act on it leaves the copy on disk forever.
func TestRepairedCopyNoticeOffersToDiscardTheCopy(t *testing.T) {
	html := renderDriveDetail(t)

	if !strings.Contains(html, "discardBackup(1)") {
		t.Error("the notice has no discard control wired to this drive")
	}
}
