package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// twoDiscSet is a drive whose disc is changed between reads, both discs
// reporting the same volume label.
type twoDiscSet struct {
	mu    sync.Mutex
	discs []*makemkv.DiscScan
	n     int
}

func (t *twoDiscSet) ScanDisc(context.Context, int) (*makemkv.DiscScan, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d := t.discs[min(t.n, len(t.discs)-1)]
	t.n++
	return d, nil
}

func waitForTitles(t *testing.T, srv *Server, want int) ScanResultJSON {
	t.Helper()
	deadline := time.After(10 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()

	var got ScanResultJSON
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d titles; last result had %d", want, len(got.Titles))
		case <-tick.C:
			rec := getScanResult(t, srv)
			if rec.Code != http.StatusOK {
				continue
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
			}
			if len(got.Titles) == want {
				return got
			}
		}
	}
}

// The bug, end to end.
//
// Perfect Blue ships a main disc and a bonus disc under one volume label. After
// ripping the main disc, putting the bonus disc in and pressing Scan showed the
// main disc's titles: the scan cache is keyed on drive index and disc name, the
// swap was quicker than the eject debounce so no drive event fired, and the
// endpoint answered from that cache without ever reading the disc.
func TestSwappingToASameLabelDiscShowsTheNewDisc(t *testing.T) {
	scanner := &twoDiscSet{discs: []*makemkv.DiscScan{
		cachedDiscScan("PERFECT_BLUE", "00800.mpls", "00801.mpls"),
		cachedDiscScan("PERFECT_BLUE", "00010.mpls", "00011.mpls", "00012.mpls"),
	}}

	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "PERFECT_BLUE"}, func(drivemanager.DriveEvent) {})
	mgr.PollOnce(context.Background())

	orch := workflow.NewOrchestrator(workflow.OrchestratorDeps{Scanner: scanner})
	srv := newTestServer(t, mgr)
	srv.orchestrator = orch
	srv.echo.POST("/drives/:id/scan", srv.handleDriveScan)
	srv.echo.GET("/drives/:id/scan", srv.handleDriveScanResult)
	orch.SetOnDiscChanged(srv.ClearDriveSession)

	// The main disc: scan it, and pick a release for it the way the user would.
	if code := postScan(t, srv).Code; code != http.StatusAccepted {
		t.Fatalf("first scan status = %d, want 202", code)
	}
	main := waitForTitles(t, srv, 2)
	if main.Titles[0].SourceFile != "00800.mpls" {
		t.Fatalf("first title source = %q, want the main disc's 00800.mpls", main.Titles[0].SourceFile)
	}
	srv.driveSessions.Set(0, &DriveSession{
		ReleaseID:   "1234",
		MediaItemID: "5678",
		MediaTitle:  "Perfect Blue",
	})

	// The discs are swapped. Nothing tells anyone: the labels match, and the
	// swap was shorter than the eject debounce.

	// Pressing Scan must read the disc rather than answer from the cache.
	if code := postScan(t, srv).Code; code != http.StatusAccepted {
		t.Fatalf("second scan status = %d, want 202", code)
	}
	bonus := waitForTitles(t, srv, 3)

	if bonus.Titles[0].SourceFile != "00010.mpls" {
		t.Errorf("first title source = %q, want the bonus disc's 00010.mpls", bonus.Titles[0].SourceFile)
	}

	// And the release chosen for the main disc must not still be attached to the
	// drive, or the bonus disc inherits it and gets filed as the feature.
	if session := srv.driveSessions.Get(0); session != nil && session.ReleaseID != "" {
		t.Errorf("the bonus disc inherited the main disc's release %q", session.ReleaseID)
	}
}
