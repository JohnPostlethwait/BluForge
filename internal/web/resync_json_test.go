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
	srv.echo.GET("/activity/state", srv.handleActivityState)

	req := httptest.NewRequest(http.MethodGet, "/activity/state", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON — the page cannot resync from HTML", ct)
	}

	// The store URL must never share a cache entry with anything else, and its
	// live rip state must never be stored. Without these, a reverse proxy or the
	// browser HTTP cache can serve this JSON body to a later request.
	if v := rec.Header().Get("Vary"); !strings.Contains(v, "Accept") {
		t.Errorf("Vary = %q, want it to include Accept", v)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store", cc)
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

// The activity page URL must return HTML even when the request asks for JSON.
// It once content-negotiated on Accept and returned the JSON store from the same
// URL as the page. A Caddy reverse proxy or the browser HTTP cache stored that
// body under the /activity key, and a later top-level navigation — Vivaldi
// reloading a slept tab — was served the JSON, so the page came back as raw text
// until a hard reload. The JSON now lives only at /activity/state.
func TestActivityPageIsHTMLEvenWhenJSONRequested(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	srv, _ := setupDashboardServer(t, mgr)
	srv.echo.GET("/activity", srv.handleActivity)

	req := httptest.NewRequest(http.MethodGet, "/activity", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q: /activity must not serve JSON, or a cache can replace the page with it", ct)
	}
	if !strings.HasPrefix(strings.TrimSpace(rec.Body.String()), "<") {
		t.Error("the /activity response is not HTML for an Accept: application/json request")
	}
}
