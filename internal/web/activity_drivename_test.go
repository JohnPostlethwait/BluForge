package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// With two drives ripping at once, the activity card named the disc but not
// the drive. The store carries the drive's name, as the dashboard shows it.
func TestActivityStoreNamesTheDriveOfAnActiveRip(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "TEST_DISC"}, nil)
	mgr.PollOnce(context.Background())

	blocker := &blockingRipExecutor{block: make(chan struct{})}
	engine := ripper.NewEngine(blocker)
	srv, _ := setupDashboardServer(t, mgr)
	srv.ripEngine = engine
	srv.echo.GET("/activity/state", srv.handleActivityState)

	job := ripper.NewJob(0, 0, "TEST_DISC", filepath.Join(t.TempDir(), "out"))
	if err := os.MkdirAll(job.OutputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := engine.Submit(job); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(blocker.block); engine.Wait() })

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&blocker.started) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the rip to start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	req := httptest.NewRequest(http.MethodGet, "/activity/state", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	var got activityStoreJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if len(got.Active) != 1 {
		t.Fatalf("active jobs = %d, want 1", len(got.Active))
	}
	if got.Active[0].DriveName != "Test Drive" {
		t.Errorf("DriveName = %q, want %q", got.Active[0].DriveName, "Test Drive")
	}
}
