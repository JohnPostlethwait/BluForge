package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

func newDiscardServer(t *testing.T) *Server {
	t.Helper()
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "RAMBO_DISC2"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	srv.orchestrator = workflow.NewOrchestrator(workflow.OrchestratorDeps{})
	srv.echo.POST("/activity/discard-backup", srv.handleActivityDiscardBackup)
	return srv
}

func postDiscard(t *testing.T, srv *Server, disc string) *httptest.ResponseRecorder {
	t.Helper()
	body := url.Values{}
	if disc != "" {
		body.Set("disc", disc)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/activity/discard-backup", strings.NewReader(body.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)
	return rec
}

// History rows name a disc, never a drive: the drive a rip ran on may have been
// renumbered or unplugged since, and acting on a stale index would delete some
// other disc's copy.
func TestDiscardFromActivityNeedsADiscName(t *testing.T) {
	srv := newDiscardServer(t)

	if code := postDiscard(t, srv, "").Code; code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 when no disc is named", code)
	}
}

// A copy that is already gone must say so rather than reporting success — the
// page hides the offer on success, and would hide a button that never worked.
func TestDiscardFromActivityReportsAMissingCopy(t *testing.T) {
	srv := newDiscardServer(t)

	if code := postDiscard(t, srv, "NEVER_SALVAGED").Code; code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a disc with no copy", code)
	}
}
