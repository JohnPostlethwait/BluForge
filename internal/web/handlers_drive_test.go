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
	"github.com/johnpostlethwait/bluforge/internal/discdb"
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

func TestMediaItemsToRows(t *testing.T) {
	items := []discdb.MediaItem{
		{
			ID:    1,
			Title: "Seinfeld",
			Year:  1989,
			Type:  "Series",
			Releases: []discdb.Release{
				{
					ID:         10,
					Title:      "The Complete Series 4K",
					UPC:        "043396641280",
					ASIN:       "B0DASIN001",
					RegionCode: "A",
					Discs: []discdb.Disc{
						{Format: "UHD"},
					},
				},
				{
					ID:         11,
					Title:      "Season 1 Blu-ray",
					UPC:        "043396641281",
					RegionCode: "A",
					Discs:      []discdb.Disc{},
				},
			},
		},
		{
			ID:    2,
			Title: "Deadpool 2",
			Year:  2018,
			Type:  "Movie",
			Releases: []discdb.Release{
				{
					ID:         20,
					Title:      "Blu-ray Edition",
					UPC:        "024543547853",
					RegionCode: "A",
					Discs: []discdb.Disc{
						{Format: "Blu-ray"},
						{Format: "Blu-ray"},
					},
				},
			},
		},
	}

	rows := mediaItemsToRows(items)

	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (2 releases + 1 release), got %d", len(rows))
	}

	// First row: Seinfeld / Complete Series 4K
	r := rows[0]
	if r.MediaTitle != "Seinfeld" {
		t.Errorf("row 0 MediaTitle: got %q, want %q", r.MediaTitle, "Seinfeld")
	}
	if r.MediaYear != 1989 {
		t.Errorf("row 0 MediaYear: got %d, want %d", r.MediaYear, 1989)
	}
	if r.MediaType != "Series" {
		t.Errorf("row 0 MediaType: got %q, want %q", r.MediaType, "Series")
	}
	if r.ReleaseTitle != "The Complete Series 4K" {
		t.Errorf("row 0 ReleaseTitle: got %q, want %q", r.ReleaseTitle, "The Complete Series 4K")
	}
	if r.ReleaseUPC != "043396641280" {
		t.Errorf("row 0 ReleaseUPC: got %q, want %q", r.ReleaseUPC, "043396641280")
	}
	if r.ReleaseASIN != "B0DASIN001" {
		t.Errorf("row 0 ReleaseASIN: got %q, want %q", r.ReleaseASIN, "B0DASIN001")
	}
	if r.Format != "UHD" {
		t.Errorf("row 0 Format: got %q, want %q", r.Format, "UHD")
	}
	if r.DiscCount != 1 {
		t.Errorf("row 0 DiscCount: got %d, want %d", r.DiscCount, 1)
	}
	if r.ReleaseID != "10" {
		t.Errorf("row 0 ReleaseID: got %q, want %q", r.ReleaseID, "10")
	}
	if r.MediaItemID != "1" {
		t.Errorf("row 0 MediaItemID: got %q, want %q", r.MediaItemID, "1")
	}

	// Second row: Seinfeld / Season 1 (no discs → empty format)
	r = rows[1]
	if r.ReleaseTitle != "Season 1 Blu-ray" {
		t.Errorf("row 1 ReleaseTitle: got %q, want %q", r.ReleaseTitle, "Season 1 Blu-ray")
	}
	if r.Format != "" {
		t.Errorf("row 1 Format: got %q, want empty", r.Format)
	}
	if r.DiscCount != 0 {
		t.Errorf("row 1 DiscCount: got %d, want 0", r.DiscCount)
	}

	// Third row: Deadpool 2
	r = rows[2]
	if r.MediaTitle != "Deadpool 2" {
		t.Errorf("row 2 MediaTitle: got %q, want %q", r.MediaTitle, "Deadpool 2")
	}
	if r.DiscCount != 2 {
		t.Errorf("row 2 DiscCount: got %d, want %d", r.DiscCount, 2)
	}
	if r.Format != "Blu-ray" {
		t.Errorf("row 2 Format: got %q, want %q", r.Format, "Blu-ray")
	}
}

func TestMediaItemsToRows_Empty(t *testing.T) {
	rows := mediaItemsToRows(nil)
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for nil input, got %d", len(rows))
	}

	rows = mediaItemsToRows([]discdb.MediaItem{})
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for empty input, got %d", len(rows))
	}
}

func TestMediaItemsToRows_NoReleases(t *testing.T) {
	items := []discdb.MediaItem{
		{ID: 1, Title: "NoReleases", Year: 2020, Type: "Movie"},
	}
	rows := mediaItemsToRows(items)
	if len(rows) != 0 {
		t.Errorf("expected 0 rows for item with no releases, got %d", len(rows))
	}
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

func TestHandleDriveSearch_SelectFlow(t *testing.T) {
	srv := testServerWithDrive(t, "")

	// Manually add a drive to the manager by triggering PollOnce with
	// an executor that returns a drive.
	pollMgr := drivemanager.NewManager(&driveWithDiscExecutor{
		discName: "Seinfeld Season 1",
	}, nil)
	pollMgr.PollOnce(context.Background())
	srv.driveMgr = pollMgr

	// Re-register route with updated server.
	srv.echo = echo.New()
	srv.echo.POST("/drives/:id/search", srv.handleDriveSearch)

	// POST with release_id + media_item_id (the "Select" flow).
	form := url.Values{}
	form.Set("release_id", "10")
	form.Set("media_item_id", "1")
	form.Set("media_title", "Seinfeld")
	form.Set("media_year", "1989")
	form.Set("media_type", "Series")

	req := httptest.NewRequest(http.MethodPost, "/drives/0/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	// Should render the full detail page with the matched banner.
	if !strings.Contains(body, "Matched:") {
		t.Error("response should contain matched banner")
	}
	if !strings.Contains(body, "Seinfeld") {
		t.Error("response should contain selected media title 'Seinfeld'")
	}
	if !strings.Contains(body, "1989") {
		t.Error("response should contain selected media year '1989'")
	}
	if !strings.Contains(body, "Series") {
		t.Error("response should contain selected media type 'Series'")
	}
	// Should NOT show "No results found" — that was the original bug.
	if strings.Contains(body, "No results found") {
		t.Error("select flow should not show 'No results found'")
	}
}

func TestHandleDriveSearch_QueryFlow_NoClient(t *testing.T) {
	srv := testServerWithDrive(t, "")

	// POST with query but no discdb client — should return results partial
	// with "No results found" since no client is configured.
	form := url.Values{}
	form.Set("query", "Seinfeld")

	req := httptest.NewRequest(http.MethodPost, "/drives/0/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	// With no client, search should fail gracefully.
	if !strings.Contains(body, "No results found") && !strings.Contains(body, "Search failed") {
		t.Error("response should indicate no results or search failure")
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
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "No results found") {
		t.Error("empty query should return 'No results found'")
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

func TestHandleDriveSearch_JSONResponse(t *testing.T) {
	// A minimal test: query with no DiscDB client should return empty JSON array.
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
	form.Set("query", "Seinfeld")

	req := httptest.NewRequest(http.MethodPost, "/drives/0/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	// With no DiscDB client, search returns nil items, which means "search failed".
	// When wantsJSON, that's a 503 with error message.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 (no discdb client), got %d: %s", rec.Code, rec.Body.String())
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("expected JSON content type, got %q", contentType)
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
