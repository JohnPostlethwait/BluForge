package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
)

// The event stream has no replay: a laptop that sleeps, or a dropped
// connection, loses whatever happened while it was away. The drive page can ask
// the server for the truth again — /drives/:id/state exists for exactly that —
// and the dashboard and activity pages could not, so they stayed stale until
// someone reloaded by hand.
//
// Both already build their whole Alpine store server-side. Serving it as JSON
// when asked for JSON is the same pattern the drive page uses.
func TestDashboardServesItsStoreAsJSON(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "TEST_DISC"}, nil)
	mgr.PollOnce(context.Background())

	srv, _ := setupDashboardServer(t, mgr)
	srv.echo.GET("/", srv.handleDashboard)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON — the page cannot resync from HTML", ct)
	}

	var got DrivesStoreJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if len(got.List) != 1 {
		t.Fatalf("got %d drives, want 1", len(got.List))
	}
	if got.List[0].RipProgress != -1 {
		t.Errorf("RipProgress = %d, want -1", got.List[0].RipProgress)
	}
	if !got.Ready {
		t.Error("Ready = false after a completed poll")
	}
}

func TestDashboardStillRendersHTMLByDefault(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	srv, _ := setupDashboardServer(t, mgr)
	srv.echo.GET("/", srv.handleDashboard)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<") {
		t.Error("the default response is not HTML")
	}
}

func TestActivityServesItsStoreAsJSON(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	srv, _ := setupDashboardServer(t, mgr)
	srv.echo.GET("/activity", srv.handleActivity)

	req := httptest.NewRequest(http.MethodGet, "/activity", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON — the page cannot resync from HTML", ct)
	}

	var got activityStoreJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if got.Active == nil || got.Pending == nil || got.History == nil {
		t.Error("the job lists must be present, not null: the page assigns them straight into the store")
	}
}

func TestActivityStillRendersHTMLByDefault(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	srv, _ := setupDashboardServer(t, mgr)
	srv.echo.GET("/activity", srv.handleActivity)

	req := httptest.NewRequest(http.MethodGet, "/activity", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<") {
		t.Error("the default response is not HTML")
	}
}
