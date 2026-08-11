package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
)

// newDriveStateServer builds a server with one drive holding the named disc,
// with routes registered so the state endpoint can be exercised end to end.
func newDriveStateServer(t *testing.T, discName string) *Server {
	t.Helper()
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: discName}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	srv.echo.GET("/drives/:id/state", srv.handleDriveState)
	return srv
}

// The drive page's only source of truth was the SSE stream. When a client
// machine suspended network I/O mid-backup the stream died, and the page sat on
// "copying, 94%" for seventy minutes after the work had finished. A client that
// reconnects needs somewhere authoritative to ask.
func TestDriveStateEndpointReturnsCurrentState(t *testing.T) {
	srv := newDriveStateServer(t, "SOME_DISC")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/drives/0/state", nil)
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var got DriveStoreJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if got.DriveIndex != 0 {
		t.Errorf("DriveIndex = %d, want 0", got.DriveIndex)
	}
	if got.DiscName != "SOME_DISC" {
		t.Errorf("DiscName = %q, want SOME_DISC", got.DiscName)
	}
	// A drive with no recovery in flight must say so, which is what clears a
	// banner left behind by a dropped connection.
	if got.RecoveryActive {
		t.Error("RecoveryActive = true with no recovery running")
	}
}

func TestDriveStateEndpointRejectsUnknownDrive(t *testing.T) {
	srv := newDriveStateServer(t, "SOME_DISC")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/drives/99/state", nil)
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a drive that does not exist", rec.Code)
	}
}
