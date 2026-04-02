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

func TestHandleDriveSelect_PersistsSession(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "TestDisc"}, nil)
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
	srv.echo.POST("/drives/:id/select", srv.handleDriveSelectAlpine)
	// Register the detail route so we can verify the session was persisted via
	// observable HTTP behavior rather than inspecting internal state directly.
	srv.echo.GET("/drives/:id", srv.handleDriveDetail)

	// Step 1: POST the selection.
	body := `{"mediaItemID":"1","releaseID":"10","title":"Seinfeld","year":"1989","type":"Series"}`
	req := httptest.NewRequest(http.MethodPost, "/drives/0/select", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("select: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify the select response itself is well-formed.
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse select response JSON: %v", err)
	}
	// scanning should be false since no orchestrator is configured.
	if scanning, ok := resp["scanning"].(bool); ok && scanning {
		t.Error("expected scanning=false when no orchestrator configured")
	}

	// Step 2: GET the drive detail page to verify session is reflected in the
	// rendered Alpine store — this confirms observable persistence behaviour.
	detailReq := httptest.NewRequest(http.MethodGet, "/drives/0", nil)
	detailRec := httptest.NewRecorder()
	srv.echo.ServeHTTP(detailRec, detailReq)

	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail: expected 200, got %d: %s", detailRec.Code, detailRec.Body.String())
	}

	detailBody := detailRec.Body.String()
	if !strings.Contains(detailBody, "Seinfeld") {
		t.Error("detail page should contain selected title after select")
	}
	if !strings.Contains(detailBody, `"releaseID":"10"`) {
		t.Error("detail page should contain selected releaseID after select")
	}
}

func TestHandleDriveSelect_InvalidDrive(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	srv := newTestServer(t, mgr)
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
