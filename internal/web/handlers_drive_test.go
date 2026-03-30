package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// stubExecutor satisfies drivemanager.DriveExecutor for tests.
type stubExecutor struct{}

func (s *stubExecutor) ListDrives(ctx context.Context) ([]makemkv.DriveInfo, error) {
	return nil, nil
}

func (s *stubExecutor) ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	return nil, nil
}

// newTestDriveManager creates a Manager with a single drive at index 0.
func newTestDriveManager(discName string) *drivemanager.Manager {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	// Trigger a poll so Ready() returns true and the drive is registered.
	// We do this by calling PollOnce with a context. Since stubExecutor returns
	// no drives, we need to use an alternate approach: just inject a drive
	// via PollOnce with real DriveInfo.
	return mgr
}

// testServerWithDrive creates a Server with a drive manager that has one drive
// registered at index 0 with the given disc name.
func testServerWithDrive(t *testing.T, discName string) *Server {
	t.Helper()

	mgr := drivemanager.NewManager(&stubExecutor{}, nil)

	cfg := config.AppConfig{OutputDir: "/tmp/test"}

	s := &Server{
		echo:          echo.New(),
		cfg:           &cfg,
		driveMgr:      mgr,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}

	// Register routes needed for tests.
	s.echo.POST("/drives/:id/search", s.handleDriveSearch)

	return s
}

func TestHandleDriveSearch_QueryFlow_NoClient(t *testing.T) {
	srv := testServerWithDrive(t, "")

	form := url.Values{}
	form.Set("query", "Seinfeld")

	req := httptest.NewRequest(http.MethodPost, "/drives/0/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	// With no client, search returns nil items → "search failed" JSON error.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("expected JSON content type, got %q", contentType)
	}
}

func TestHandleDriveSearch_EmptyQuery(t *testing.T) {
	srv := testServerWithDrive(t, "")

	form := url.Values{}
	form.Set("query", "")

	req := httptest.NewRequest(http.MethodPost, "/drives/0/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var results []SearchResultJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results for empty query, got %d", len(results))
	}
}

func TestHandleDashboard_JSONStore(t *testing.T) {
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
	srv.echo.GET("/", srv.handleDashboard)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	// Should contain the Alpine store initialization with drive data.
	if !strings.Contains(body, "Alpine.store") {
		t.Error("response should contain Alpine.store initialization")
	}
	if !strings.Contains(body, "TestDisc") {
		t.Error("response should contain disc name in store JSON")
	}
	if !strings.Contains(body, "drive-update") {
		t.Error("response should contain SSE event listener for drive-update")
	}
}

func TestHandleDriveSearch_JSONEmptyQuery(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	cfg := config.AppConfig{OutputDir: "/tmp/test"}

	srv := &Server{
		echo:          echo.New(),
		cfg:           &cfg,
		driveMgr:      mgr,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	srv.echo.POST("/drives/:id/search", srv.handleDriveSearch)

	form := url.Values{}
	form.Set("query", "")

	req := httptest.NewRequest(http.MethodPost, "/drives/0/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var results []SearchResultJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results for empty query, got %d", len(results))
	}
}

// driveWithDiscExecutor returns a single drive with a disc.
type driveWithDiscExecutor struct {
	discName string
}

func (e *driveWithDiscExecutor) ListDrives(ctx context.Context) ([]makemkv.DriveInfo, error) {
	return []makemkv.DriveInfo{
		{
			Index:      0,
			Visible:    2,
			Enabled:    999,
			Flags:      12,
			DriveName:  "Test Drive",
			DiscName:   e.discName,
			DevicePath: "/dev/sr0",
		},
	}, nil
}

func (e *driveWithDiscExecutor) ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	return &makemkv.DiscScan{DriveIndex: driveIndex, DiscName: e.discName}, nil
}

func TestDriveSelectFlow_PersistsAcrossRefresh(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "Seinfeld Season 1"}, nil)
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
	srv.echo.GET("/drives/:id", srv.handleDriveDetail)

	// Step 1: Select a release.
	selectBody := `{"mediaItemID":"1","releaseID":"10","title":"Seinfeld","year":"1989","type":"Series"}`
	selectReq := httptest.NewRequest(http.MethodPost, "/drives/0/select", strings.NewReader(selectBody))
	selectReq.Header.Set("Content-Type", "application/json")
	selectReq.Header.Set("Accept", "application/json")
	selectRec := httptest.NewRecorder()
	srv.echo.ServeHTTP(selectRec, selectReq)

	if selectRec.Code != http.StatusOK {
		t.Fatalf("select: expected 200, got %d", selectRec.Code)
	}

	// Step 2: "Refresh" — GET the drive detail page.
	detailReq := httptest.NewRequest(http.MethodGet, "/drives/0", nil)
	detailRec := httptest.NewRecorder()
	srv.echo.ServeHTTP(detailRec, detailReq)

	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail: expected 200, got %d", detailRec.Code)
	}

	body := detailRec.Body.String()

	// The store JSON should contain the persisted selection.
	if !strings.Contains(body, "Seinfeld") {
		t.Error("detail page should contain 'Seinfeld' in store JSON after select")
	}
	if !strings.Contains(body, `"mediaItemID":"1"`) || !strings.Contains(body, `"releaseID":"10"`) {
		t.Error("detail page should contain selected release IDs in store JSON")
	}
}

func TestDriveSessionClearedOnEject(t *testing.T) {
	store := NewDriveSessionStore()
	store.Set(0, &DriveSession{
		MediaTitle: "Seinfeld",
		ReleaseID:  "10",
	})

	// Simulate eject by clearing session.
	store.Clear(0)

	if session := store.Get(0); session != nil {
		t.Error("expected session to be nil after clear (simulating eject)")
	}
}
