package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
)

func TestHandleDriveSelect_PersistsSession(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "TestDisc"}, nil)
	mgr.PollOnce(context.Background())

	cfg := config.AppConfig{OutputDir: "/tmp/test"}
	srv := &Server{
		echo:          echo.New(),
		cfg:           &cfg,
		driveMgr:      mgr,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	srv.echo.POST("/drives/:id/select", srv.handleDriveSelectAlpine)

	body := `{"mediaItemID":"1","releaseID":"10","title":"Seinfeld","year":"1989","type":"Series"}`
	req := httptest.NewRequest(http.MethodPost, "/drives/0/select", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify session was persisted.
	session := srv.driveSessions.Get(0)
	if session == nil {
		t.Fatal("expected drive session to be persisted")
	}
	if session.MediaTitle != "Seinfeld" {
		t.Errorf("MediaTitle: got %q, want %q", session.MediaTitle, "Seinfeld")
	}
	if session.ReleaseID != "10" {
		t.Errorf("ReleaseID: got %q, want %q", session.ReleaseID, "10")
	}

	// Verify JSON response.
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	// scanning should be false since no orchestrator is configured.
	if scanning, ok := resp["scanning"].(bool); ok && scanning {
		t.Error("expected scanning=false when no orchestrator configured")
	}
}

func TestHandleDriveSelect_InvalidDrive(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)

	cfg := config.AppConfig{OutputDir: "/tmp/test"}
	srv := &Server{
		echo:          echo.New(),
		cfg:           &cfg,
		driveMgr:      mgr,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	srv.echo.POST("/drives/:id/select", srv.handleDriveSelectAlpine)

	body := `{"mediaItemID":"1","releaseID":"10","title":"Test","year":"2020","type":"Movie"}`
	req := httptest.NewRequest(http.MethodPost, "/drives/99/select", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown drive, got %d", rec.Code)
	}
}
