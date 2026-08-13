package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// The drive number on a failed job was recorded when the rip ran. Optical
// devices renumber: a USB drive that reconnected moved RAMBO_DISC2 from index 0
// to index 1, and salvaging by the recorded number ran against an empty drive
// and failed in four seconds with a confusing message about a path in /mnt.
func TestSalvageFollowsTheDiscRatherThanTheRecordedDrive(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "RAMBO_DISC2"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	srv.orchestrator = workflow.NewOrchestrator(workflow.OrchestratorDeps{})
	srv.echo.POST("/drives/:id/salvage", srv.handleDriveSalvage)

	idx, ok := srv.driveHoldingDisc("RAMBO_DISC2")
	if !ok {
		t.Fatal("the disc was not found in any drive")
	}
	if idx != 0 {
		t.Errorf("driveHoldingDisc = %d, want 0 for this fixture", idx)
	}
}

// A disc that is not in any drive cannot be salvaged, and saying so beats
// running a two-hour operation against whatever happens to hold that number.
func TestSalvageRefusesWhenTheDiscIsNotInAnyDrive(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "RAMBO_DISC2"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	srv.orchestrator = workflow.NewOrchestrator(workflow.OrchestratorDeps{})
	srv.echo.POST("/drives/:id/salvage", srv.handleDriveSalvage)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/drives/0/salvage?disc=SOME_OTHER_DISC", nil)
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 for a disc that is not loaded (body: %s)", rec.Code, rec.Body.String())
	}
}

// Without a disc name there is nothing to check against, and the recorded
// number is all there is.
func TestSalvageWithoutADiscNameUsesTheGivenDrive(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "RAMBO_DISC2"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	srv.orchestrator = workflow.NewOrchestrator(workflow.OrchestratorDeps{})
	srv.echo.POST("/drives/:id/salvage", srv.handleDriveSalvage)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/drives/0/salvage", nil)
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)

	// This orchestrator has no backupper, so it refuses — but not with the
	// conflict that means "that disc is nowhere".
	if rec.Code == http.StatusConflict {
		t.Error("a salvage with no disc name was refused as though the disc were missing")
	}
}

func TestDriveHoldingDiscIgnoresEmptyDrives(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: ""}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	if _, ok := srv.driveHoldingDisc(""); ok {
		t.Error("an empty drive was reported as holding a disc")
	}
}
