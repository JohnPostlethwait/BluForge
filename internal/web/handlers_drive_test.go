package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// testServerWithDrive creates a Server backed by a stub (no-drives) manager,
// suitable for search-handler tests that don't require a real disc present.
func testServerWithDrive(t *testing.T, discName string) *Server {
	t.Helper()

	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	s := newTestServer(t, mgr)

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

	srv := newTestServer(t, mgr)
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
	srv := newTestServer(t, mgr)
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

func TestDriveSelectFlow_PersistsAcrossRefresh(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "Seinfeld Season 1"}, nil)
	mgr.PollOnce(context.Background())

	srv := newTestServer(t, mgr)
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

func TestHandleDriveMatch_ReturnsEnrichedTitles(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "Seinfeld_Season_1"}, nil)
	mgr.PollOnce(context.Background())

	orch := workflow.NewOrchestrator(workflow.OrchestratorDeps{})

	srv := newTestServer(t, mgr)
	srv.orchestrator = orch
	srv.echo.POST("/drives/:id/match", srv.handleDriveMatch)

	// Pre-populate: cached scan in orchestrator.
	scan := &makemkv.DiscScan{
		DiscName:   "Seinfeld_Season_1",
		TitleCount: 2,
		Titles: []makemkv.TitleInfo{
			{Index: 0, Attributes: map[int]string{2: "Title 0", 9: "0:23:01", 11: "10.9 GB", 16: "00001.mpls"}},
			{Index: 1, Attributes: map[int]string{2: "Title 1", 9: "0:02:17", 11: "312.6 MB", 16: "99999.mpls"}},
		},
	}
	orch.InjectCachedScan(0, scan)

	// Pre-populate: session with raw search results.
	srv.driveSessions.Set(0, &DriveSession{
		ReleaseID: "10",
		RawSearchResults: []discdb.MediaItem{
			{
				ID: 1, Title: "Seinfeld", Type: "series",
				Releases: []discdb.Release{
					{
						ID: 10,
						Discs: []discdb.Disc{
							{
								ID: 100,
								Titles: []discdb.DiscTitle{
									{SourceFile: "00001.mpls", ItemType: "series", Season: "1", Episode: "1",
										Item: &discdb.DiscItemReference{Title: "The Seinfeld Chronicles"}},
								},
							},
						},
					},
				},
			},
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/drives/0/match", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var titles []TitleJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &titles); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(titles) != 2 {
		t.Fatalf("expected 2 titles, got %d", len(titles))
	}

	// Find by index.
	byIdx := make(map[int]TitleJSON)
	for _, tj := range titles {
		byIdx[tj.Index] = tj
	}

	if !byIdx[0].Matched {
		t.Error("title 0 should be matched")
	}
	if byIdx[0].ContentTitle != "The Seinfeld Chronicles" {
		t.Errorf("title 0 ContentTitle: expected \"The Seinfeld Chronicles\", got %q", byIdx[0].ContentTitle)
	}
	if byIdx[0].Season != "1" || byIdx[0].Episode != "1" {
		t.Errorf("title 0 S/E: expected 1/1, got %s/%s", byIdx[0].Season, byIdx[0].Episode)
	}
	if !byIdx[0].Selected {
		t.Error("title 0 should be selected (matched)")
	}

	if byIdx[1].Matched {
		t.Error("title 1 should NOT be matched")
	}
	if byIdx[1].Selected {
		t.Error("title 1 should NOT be selected (unmatched)")
	}
}

func TestHandleDriveDetail_ActiveRipSetsStoreFields(t *testing.T) {
	// Set up a drive manager with a disc loaded.
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "THE_BFG"}, nil)
	mgr.PollOnce(context.Background())

	// Set up a blocking rip engine with one active job on drive index 0.
	// blockingRipExecutor is defined in handlers_activity_test.go (same package).
	blocker := &blockingRipExecutor{block: make(chan struct{})}
	engine := ripper.NewEngine(blocker)

	tmpDir := t.TempDir()
	job1 := ripper.NewJob(0, 0, "THE_BFG", tmpDir+"/out1")
	job1.ID = 1
	jobDone := make(chan struct{})
	job1.OnComplete = func(_ *ripper.Job, _ error) { close(jobDone) }
	if err := os.MkdirAll(job1.OutputDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	engine.Submit(job1)

	// Wait for the job to start so it appears in engine.ActiveJobs().
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&blocker.started) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for job to start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	srv := newTestServer(t, mgr)
	srv.ripEngine = engine

	e := echo.New()
	e.GET("/drives/:id", srv.handleDriveDetail)
	req := httptest.NewRequest(http.MethodGet, "/drives/0", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"ripActive":true`) {
		t.Error("expected ripActive:true in hydrated store JSON")
	}
	if !strings.Contains(body, `"activeJobCount":1`) {
		t.Error("expected activeJobCount:1 in hydrated store JSON")
	}

	// Release blocker and wait for the engine goroutine to finish writing files
	// before t.TempDir cleanup runs.
	close(blocker.block)
	<-jobDone
}
