package web

import (
	"context"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
)

// A release selection belongs to the disc it was chosen for. Nothing bound it
// to that disc, so a swap left it applying to whatever came next: ripping Akira
// and then loading Disclosure Day showed "Matched: Akira (1988)" on the new
// disc's page, with the titles that follow named from it.
//
// The two things that were supposed to prevent this both miss a normal swap.
// EventDiscEjected is only believed after the drive reports empty for a
// continuous 30s (ejectConfirmDuration), which taking one disc out and putting
// the next in never reaches — and the insert path clears the absence timer
// outright. The orchestrator's onDiscChanged fires only from a scan, and only
// when a previous scan is cached to compare against; the insert event calls
// InvalidateScan first, so there is nothing left to compare and it returns
// early.
//
// Neither is load-bearing here. The drive knows what disc it holds, so the
// session can be checked against it directly on every render.
func TestASelectionDoesNotFollowTheDiscThatReplacedIt(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "DISCLOSURE_DAY"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)

	// The selection the user made while Akira was in the drive.
	srv.driveSessions.Set(0, &DriveSession{
		DiscLabel:   "AKIRA",
		MediaItemID: "1",
		ReleaseID:   "10",
		MediaTitle:  "Akira",
		MediaYear:   "1988",
		MediaType:   "Movie",
		ReleaseUPC:  "704400103612",
		ReleaseASIN: "B09JXWP8N1",
	})

	drv := mgr.GetDrive(0)
	if drv == nil {
		t.Fatal("no drive 0")
	}
	if drv.DiscName() != "DISCLOSURE_DAY" {
		t.Fatalf("drive holds %q, want DISCLOSURE_DAY — the test is not set up as intended", drv.DiscName())
	}

	got := srv.buildDriveStore(0, drv)

	if got.SelectedRelease != nil {
		t.Errorf("SelectedRelease = %+v, want nil — %q's match is being shown for the disc that replaced it",
			got.SelectedRelease, "AKIRA")
	}
	if got.CurrentStep != 1 {
		t.Errorf("CurrentStep = %d, want 1 — the wizard resumed mid-flow on a disc it knows nothing about", got.CurrentStep)
	}
}

// The stale session must be dropped, not merely hidden. Leaving it in place
// would let the next render — or any handler reading the session directly, such
// as match or rip — pick the old release back up.
func TestAStaleSessionIsDroppedNotJustHidden(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "DISCLOSURE_DAY"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	srv.driveSessions.Set(0, &DriveSession{
		DiscLabel:  "AKIRA",
		ReleaseID:  "10",
		MediaTitle: "Akira",
	})

	drv := mgr.GetDrive(0)
	if drv == nil {
		t.Fatal("no drive 0")
	}
	srv.buildDriveStore(0, drv)

	if session := srv.driveSessions.Get(0); session != nil && session.ReleaseID != "" {
		t.Errorf("session still holds ReleaseID %q after rendering against a different disc", session.ReleaseID)
	}
}

// The ordinary case must keep working: a session for the disc that is actually
// in the drive is not stale and must survive a refresh.
func TestASelectionSurvivesForTheDiscItWasMadeFor(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "DISCLOSURE_DAY"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	srv.driveSessions.Set(0, &DriveSession{
		DiscLabel:  "DISCLOSURE_DAY",
		ReleaseID:  "77",
		MediaTitle: "Disclosure Day",
		MediaYear:  "2024",
		MediaType:  "Movie",
	})

	drv := mgr.GetDrive(0)
	if drv == nil {
		t.Fatal("no drive 0")
	}
	got := srv.buildDriveStore(0, drv)

	if got.SelectedRelease == nil {
		t.Fatal("SelectedRelease = nil; the selection for the disc actually in the drive was discarded")
	}
	if got.SelectedRelease.ReleaseID != "77" {
		t.Errorf("ReleaseID = %q, want 77", got.SelectedRelease.ReleaseID)
	}
}

// A session recorded before this binding existed has no disc name. It cannot be
// proven stale, and discarding every one of them would clear the selection of
// anyone mid-flow across an upgrade or a restart. Keep it.
func TestASessionWithNoRecordedDiscIsKept(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "DISCLOSURE_DAY"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	srv.driveSessions.Set(0, &DriveSession{
		ReleaseID:  "77",
		MediaTitle: "Something",
	})

	drv := mgr.GetDrive(0)
	if drv == nil {
		t.Fatal("no drive 0")
	}
	got := srv.buildDriveStore(0, drv)

	if got.SelectedRelease == nil {
		t.Error("SelectedRelease = nil; a session with no recorded disc was treated as stale")
	}
}

// An empty drive must not clear the session.
//
// makemkvcon reports a drive as empty while it is opening the disc for a long
// operation, which is why an eject is only believed after the absence has
// lasted (ejectConfirmDuration, added in 2eb016b after a spurious eject cleared
// a selection mid-backup). Treating "no disc name" as "a different disc" here
// would reintroduce exactly that: a mid-rip poll returning empty would discard
// the user's release. Only a drive reporting a *different* disc is proof.
func TestAnEmptyDriveDoesNotClearTheSession(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, func(drivemanager.DriveEvent) {})
	srv := newTestServer(t, mgr)

	srv.driveSessions.Set(0, &DriveSession{
		DiscLabel:  "AKIRA",
		ReleaseID:  "10",
		MediaTitle: "Akira",
	})

	// A drive that is reporting no disc — transiently or otherwise.
	drv := drivemanager.NewDriveState(0, "/dev/sr0")
	if drv.DiscName() != "" {
		t.Fatalf("DiscName = %q, want empty — the test is not set up as intended", drv.DiscName())
	}

	got := srv.buildDriveStore(0, drv)

	if got.SelectedRelease == nil {
		t.Error("SelectedRelease = nil; a drive reporting empty discarded the selection, which is the mid-backup bug 2eb016b fixed")
	}
	if session := srv.driveSessions.Get(0); session == nil || session.ReleaseID != "10" {
		t.Error("the session was cleared by an empty drive reading")
	}
}
