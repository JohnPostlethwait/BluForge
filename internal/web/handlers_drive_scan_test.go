package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// blockingScanner never finishes, which is what a scan of a damaged disc looks
// like from the browser's side.
type blockingScanner struct{ release chan struct{} }

func (b *blockingScanner) ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &makemkv.DiscScan{DiscName: "SOME_DISC"}, nil
}

func newScanServer(t *testing.T, scanner workflow.DiscScanner) (*Server, *workflow.Orchestrator) {
	t.Helper()
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "SOME_DISC"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	orch := workflow.NewOrchestrator(workflow.OrchestratorDeps{Scanner: scanner})
	srv := newTestServer(t, mgr)
	srv.orchestrator = orch
	srv.echo.POST("/drives/:id/scan", srv.handleDriveScan)
	srv.echo.GET("/drives/:id/scan", srv.handleDriveScanResult)
	srv.echo.GET("/drives/:id/state", srv.handleDriveState)
	return srv, orch
}

func postScan(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/drives/0/scan", nil)
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)
	return rec
}

// The scan held the request open for as long as it ran. On Police Story 2 that
// was thirty minutes of a page that said nothing, and when the browser gave up
// it took makemkvcon with it.
func TestScanReturnsImmediatelyRatherThanHoldingTheRequest(t *testing.T) {
	scanner := &blockingScanner{release: make(chan struct{})}
	srv, orch := newScanServer(t, scanner)
	defer close(scanner.release)

	done := make(chan int, 1)
	go func() { done <- postScan(t, srv).Code }()

	select {
	case code := <-done:
		if code != http.StatusAccepted {
			t.Errorf("status = %d, want 202 while the scan runs", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request blocked on the scan")
	}

	if !orch.ScanStatus(0).Active {
		t.Error("the scan was not started")
	}
}

// Once the scan is cached, GET on the same path returns the titles — this is
// what the page re-fetches when it sees the done event. POST no longer answers
// from cache: it is the request to read the disc. See
// TestPressingScanReadsTheDiscEvenWithACachedScan.
func TestScanResultReturnsTitlesOnceTheScanIsCached(t *testing.T) {
	srv, orch := newScanServer(t, &blockingScanner{release: make(chan struct{})})
	orch.InjectCachedScan(0, &makemkv.DiscScan{
		DiscName:   "SOME_DISC",
		TitleCount: 1,
		Titles: []makemkv.TitleInfo{
			{Index: 0, Attributes: map[int]string{2: "Feature", 9: "1:52:00", 16: "00800.mpls"}},
		},
	})

	rec := getScanResult(t, srv)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	var got ScanResultJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if len(got.Titles) != 1 {
		t.Errorf("got %d titles, want 1", len(got.Titles))
	}
}

// A second request while a scan runs must not start a second makemkvcon, and
// must not read as an error either.
func TestSecondScanRequestIsAccepted(t *testing.T) {
	scanner := &blockingScanner{release: make(chan struct{})}
	srv, _ := newScanServer(t, scanner)
	defer close(scanner.release)

	if code := postScan(t, srv).Code; code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", code)
	}
	if code := postScan(t, srv).Code; code != http.StatusAccepted {
		t.Errorf("second status = %d, want 202", code)
	}
}

// A page that reconnects mid-scan has missed every event so far. Without this
// it shows an idle drive while makemkvcon is still reading.
func TestDriveStateReportsAnInFlightScan(t *testing.T) {
	scanner := &blockingScanner{release: make(chan struct{})}
	srv, _ := newScanServer(t, scanner)
	defer close(scanner.release)

	postScan(t, srv)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/drives/0/state", nil)
	req.Header.Set("Accept", "application/json")
	srv.echo.ServeHTTP(rec, req)

	var got DriveStoreJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if !got.ScanActive {
		t.Error("ScanActive = false while a scan is running")
	}
	if got.ScanStartedAt == 0 {
		t.Error("ScanStartedAt = 0; the page has nothing to count elapsed time from")
	}
}
