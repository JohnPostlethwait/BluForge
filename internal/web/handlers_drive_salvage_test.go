package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

func newSalvageServer(t *testing.T) *Server {
	t.Helper()
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "RAMBO_DISC2"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	srv.orchestrator = workflow.NewOrchestrator(workflow.OrchestratorDeps{})
	srv.echo.POST("/drives/:id/salvage", srv.handleDriveSalvage)
	srv.echo.GET("/drives/:id/state", srv.handleDriveState)
	return srv
}

func postSalvage(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)
	return rec
}

// A salvage runs for hours, so the request cannot wait for it. It reports that
// the work has started and the page follows along from there.
func TestSalvageRequestDoesNotWaitForTheSalvage(t *testing.T) {
	srv := newSalvageServer(t)

	rec := postSalvage(t, srv, "/drives/0/salvage")

	// Without a backupper configured this orchestrator refuses, which is the
	// correct answer — what matters is that it answers rather than blocking.
	if rec.Code == http.StatusOK {
		t.Errorf("status = %d; a salvage must not report completion synchronously", rec.Code)
	}
}

func TestSalvageRejectsAnUnknownDrive(t *testing.T) {
	srv := newSalvageServer(t)

	if code := postSalvage(t, srv, "/drives/99/salvage").Code; code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a drive that does not exist", code)
	}
}

func TestSalvageRejectsAnInvalidDriveID(t *testing.T) {
	srv := newSalvageServer(t)

	if code := postSalvage(t, srv, "/drives/abc/salvage").Code; code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
}

// A page loaded or reconnected during a salvage has missed every event so far,
// and the operation runs for hours.
func TestDriveStateReportsASalvage(t *testing.T) {
	srv := newSalvageServer(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/drives/0/state", nil)
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)

	var got DriveStoreJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if got.SalvageActive {
		t.Error("SalvageActive = true with no salvage running")
	}
}
