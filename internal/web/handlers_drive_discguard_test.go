package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

func postRip(t *testing.T, srv *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/drives/0/rip", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)
	return rec
}

func ripServerWithDisc(t *testing.T, discName string) *Server {
	t.Helper()
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: discName}, nil)
	mgr.PollOnce(context.Background())

	srv, store := setupDashboardServer(t, mgr)
	srv.orchestrator = workflow.NewOrchestrator(workflow.OrchestratorDeps{
		Store:     store,
		Engine:    ripper.NewEngine(&immediateRipExecutor{}),
		Organizer: organizer.New(),
	})
	srv.echo.POST("/drives/:id/rip", srv.handleDriveRip)
	return srv
}

func ripServer(t *testing.T) *Server {
	t.Helper()
	return ripServerWithDisc(t, "DISC_IN_DRIVE")
}

// The rip form carries the disc name the page was built from, and the handler
// trusted it. Swap the disc between rendering the page and pressing Rip — a
// box set, one disc after another — and the new disc is ripped into the old
// disc's filenames, and a mapping is saved claiming it is that film.
//
// Everything else in this codebase binds state to the disc rather than the
// drive. This path did not.
func TestRipIsRefusedWhenTheDiscHasChangedSinceThePageLoaded(t *testing.T) {
	srv := ripServer(t)

	form := url.Values{}
	form.Set("disc_name", "A_DIFFERENT_DISC")
	form.Add("titles", "0")

	rec := postRip(t, srv, form)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "error") {
		t.Fatalf("the rip was accepted for a disc that is not in the drive: %q", loc)
	}
	if !strings.Contains(strings.ToLower(loc), "disc") {
		t.Errorf("the error does not mention the disc: %q", loc)
	}
}

// The ordinary case has to keep working: the form names the disc that is
// actually loaded.
func TestRipProceedsWhenTheDiscStillMatches(t *testing.T) {
	srv := ripServer(t)

	form := url.Values{}
	form.Set("disc_name", "DISC_IN_DRIVE")
	form.Add("titles", "0")

	rec := postRip(t, srv, form)
	loc := rec.Header().Get("Location")

	// It may still fail for unrelated reasons in this stub setup, but it must
	// not be rejected for the disc having changed.
	if strings.Contains(strings.ToLower(loc), "no longer in the drive") {
		t.Errorf("a matching disc was rejected as changed: %q", loc)
	}
}

// A drive that reports no volume label — plenty of discs have none — cannot be
// compared against, and must not be blocked on that basis.
func TestRipIsNotBlockedWhenTheDriveReportsNoLabel(t *testing.T) {
	srv := ripServerWithDisc(t, "")

	form := url.Values{}
	form.Set("disc_name", "SOMETHING")
	form.Add("titles", "0")

	rec := postRip(t, srv, form)
	loc := rec.Header().Get("Location")

	if strings.Contains(strings.ToLower(loc), "no longer in the drive") {
		t.Errorf("an unlabelled drive was treated as a disc mismatch: %q", loc)
	}
}
