package web

import (
	"context"
	"net/http"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// newScanResultServer builds a server with a real store, an orchestrator
// holding a cached scan, and a drive session with a release already chosen.
func newScanResultServer(t *testing.T) (*Server, *db.Store, string) {
	t.Helper()

	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "SOME_DISC"}, nil)
	mgr.PollOnce(context.Background())

	srv, store := setupDashboardServer(t, mgr)
	orch := workflow.NewOrchestrator(workflow.OrchestratorDeps{Store: store})
	srv.orchestrator = orch
	srv.echo.GET("/drives/:id/scan", srv.handleDriveScanResult)

	scan := cachedDiscScan("SOME_DISC", "00800.mpls")
	orch.SetDriveDisc(0, "SOME_DISC")
	orch.InjectCachedScan(0, scan)

	srv.driveSessions.Set(0, &DriveSession{
		DiscLabel:   "SOME_DISC",
		MediaItemID: "item-1",
		ReleaseID:   "rel-1",
		DiscID:      "disc-1",
		MediaTitle:  "Some Movie",
		MediaYear:   "2024",
		MediaType:   "movie",
	})

	return srv, store, discdb.BuildDiscKey(scan)
}

// Fetching the titles of a finished scan is a read. It used to also upsert a
// disc mapping, which meant that merely searching, picking a release and
// scanning — without ever pressing Rip — permanently taught BluForge that this
// disc is that film. The page then greeted the disc with "Previously matched"
// on every later visit, and auto-rip acted on it.
//
// The mapping is written when a rip is actually submitted. That is the point at
// which the user has confirmed the match.
func TestScanResultDoesNotSaveAMappingTheUserNeverConfirmed(t *testing.T) {
	srv, store, discKey := newScanResultServer(t)

	if rec := getScanResult(t, srv); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	mapping, err := store.GetMapping(discKey)
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if mapping != nil {
		t.Errorf("fetching scan results saved a mapping for %q (%+v); "+
			"only a submitted rip should record a match", discKey, mapping)
	}
}

// The endpoint still has to do its actual job: serve the titles, enriched with
// the selected release's match data.
func TestScanResultStillServesTitles(t *testing.T) {
	srv, _, _ := newScanResultServer(t)

	rec := getScanResult(t, srv)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); body == "" {
		t.Error("no titles served")
	}
}
