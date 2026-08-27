package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// newClearServer builds the situation the user is stuck in: a release picked
// from TheDiscDB that turned out to be the wrong one, a scan cached against the
// disc, and a mapping saved from an earlier rip of it.
func newClearServer(t *testing.T) (*Server, *workflow.Orchestrator, *db.Store, string) {
	t.Helper()

	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "SOME_DISC"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	orch := workflow.NewOrchestrator(workflow.OrchestratorDeps{Store: store})
	scan := cachedDiscScan("SOME_DISC", "00800.mpls")
	orch.InjectCachedScan(0, scan)

	discKey := discdb.BuildDiscKey(scan)
	if err := store.SaveMapping(db.DiscMapping{
		DiscKey:     discKey,
		DiscName:    "SOME_DISC",
		MediaItemID: "1",
		ReleaseID:   "10",
		MediaTitle:  "The Wrong Film",
		MediaYear:   "1999",
		MediaType:   "Movie",
	}); err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}

	srv := newTestServer(t, mgr)
	srv.store = store
	srv.orchestrator = orch
	srv.driveSessions.Set(0, &DriveSession{
		MediaItemID:   "1",
		ReleaseID:     "10",
		MediaTitle:    "The Wrong Film",
		MediaYear:     "1999",
		MediaType:     "Movie",
		SearchResults: []SearchResultJSON{{ReleaseID: "10", MediaTitle: "The Wrong Film"}},
	})

	srv.echo.POST("/drives/:id/clear-match", srv.handleDriveClearMatch)
	srv.echo.GET("/drives/:id/state", srv.handleDriveState)

	return srv, orch, store, discKey
}

func postClearMatch(t *testing.T, srv *Server, id string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/drives/"+id+"/clear-match", nil)
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)
	return rec
}

// The whole point of the endpoint: a selection that turned out to be wrong has
// to be removable. Until now it survived every refresh and could only be
// dislodged by ejecting the disc.
func TestClearMatchDropsTheReleaseSelection(t *testing.T) {
	srv, _, _, _ := newClearServer(t)

	if rec := postClearMatch(t, srv, "0"); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	got := driveState(t, srv)
	if got.SelectedRelease != nil {
		t.Errorf("SelectedRelease = %+v, want nil — the wrong match survived the clear", got.SelectedRelease)
	}
	if len(got.SearchResults) != 0 {
		t.Errorf("got %d search results, want 0 — the search that produced the wrong match is still cached", len(got.SearchResults))
	}
}

// Clearing has to put the wizard back at Search. Left on a later step the user
// is looking at a page built around the match they just discarded.
func TestClearMatchReturnsTheWizardToSearch(t *testing.T) {
	srv, _, _, _ := newClearServer(t)
	postClearMatch(t, srv, "0")

	if got := driveState(t, srv).CurrentStep; got != 1 {
		t.Errorf("CurrentStep = %d, want 1", got)
	}
}

// "Re-scan the disc without it" means the next scan reads the disc. A cached
// title list left in place would be served instead.
func TestClearMatchInvalidatesTheCachedScan(t *testing.T) {
	srv, orch, _, _ := newClearServer(t)
	postClearMatch(t, srv, "0")

	if scan := orch.GetCachedScanByDrive(0); scan != nil {
		t.Errorf("cached scan = %+v, want nil — the next scan would answer from cache", scan)
	}
	if got := driveState(t, srv).ScanCachedAt; got != 0 {
		t.Errorf("ScanCachedAt = %d, want 0", got)
	}
}

// A mapping saved by an earlier rip resurfaces as "Previously matched" on every
// later visit. Leaving it behind means the wrong answer comes straight back.
func TestClearMatchDeletesTheSavedDiscMapping(t *testing.T) {
	srv, _, store, discKey := newClearServer(t)
	postClearMatch(t, srv, "0")

	mapping, err := store.GetMapping(discKey)
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if mapping != nil {
		t.Errorf("mapping = %+v, want nil — the disc still remembers the wrong match", mapping)
	}
	if driveState(t, srv).HasMapping {
		t.Error("HasMapping = true; the page would still offer the discarded match")
	}
}

// The mapping is keyed on the cached scan, so it must be deleted before the
// scan is dropped. Getting the order wrong loses the key and silently leaves
// the row behind — which looks like success from the browser.
func TestClearMatchDeletesTheMappingBeforeLosingTheKey(t *testing.T) {
	srv, orch, store, discKey := newClearServer(t)

	postClearMatch(t, srv, "0")

	if orch.GetCachedScanByDrive(0) != nil {
		t.Fatal("cached scan survived; this test cannot tell the ordering apart")
	}
	mapping, err := store.GetMapping(discKey)
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if mapping != nil {
		t.Error("the mapping outlived the scan it is keyed on — deleted after the key was gone")
	}
}

// Pressing clear twice, or on a disc that was never matched, is not an error.
func TestClearMatchWithNothingToClearSucceeds(t *testing.T) {
	srv, _, _, _ := newClearServer(t)

	postClearMatch(t, srv, "0")
	if rec := postClearMatch(t, srv, "0"); rec.Code != http.StatusOK {
		t.Errorf("second clear: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestClearMatchRejectsANonNumericDriveID(t *testing.T) {
	srv, _, _, _ := newClearServer(t)

	if rec := postClearMatch(t, srv, "abc"); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestClearMatchRejectsAnUnknownDrive(t *testing.T) {
	srv, _, _, _ := newClearServer(t)

	if rec := postClearMatch(t, srv, "99"); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Every other test here registers the route by hand, so none of them would
// notice a path typo in NewServer or a CSRF rejection. This drives the real
// router the browser talks to.
func TestClearMatchIsReachableThroughTheRealRouter(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "SOME_DISC"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	srv := NewServer(ServerDeps{
		Config:   &config.AppConfig{OutputDir: t.TempDir()},
		DriveMgr: mgr,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/drives/0/clear-match", nil)
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the button on the page would fail (body: %s)",
			rec.Code, rec.Body.String())
	}
}

// Clearing one drive must not disturb another drive's selection.
func TestClearMatchLeavesOtherDrivesAlone(t *testing.T) {
	srv, _, _, _ := newClearServer(t)
	srv.driveSessions.Set(1, &DriveSession{ReleaseID: "77", MediaTitle: "Another Disc"})

	postClearMatch(t, srv, "0")

	other := srv.driveSessions.Get(1)
	if other == nil || other.ReleaseID != "77" {
		t.Errorf("drive 1 session = %+v, want ReleaseID 77 intact", other)
	}
}
