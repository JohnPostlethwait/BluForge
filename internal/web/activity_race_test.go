package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// progressingRipExecutor keeps reporting progress until it is told to stop, so
// a test can read the job while the rip goroutine is actively mutating it.
type progressingRipExecutor struct {
	started int32
	stop    chan struct{}
}

func (p *progressingRipExecutor) StartRip(_ context.Context, _ makemkv.Source, _ int, _ string, outputDir string, onEvent func(makemkv.Event), _ *makemkv.SelectionOpts) error {
	atomic.StoreInt32(&p.started, 1)
	pct := 0
	for {
		select {
		case <-p.stop:
			_ = os.WriteFile(filepath.Join(outputDir, "title.mkv"), []byte("fake"), 0o644)
			return nil
		default:
		}
		pct = (pct + 1) % 100
		if onEvent != nil {
			onEvent(makemkv.Event{
				Type:     "PRGV",
				Progress: &makemkv.Progress{Current: pct, Total: pct, Max: 100},
			})
		}
	}
}

// The activity page reads Status, Progress, Error, StartedAt and TrackMetadata
// straight off the *ripper.Job pointers that ActiveJobs returns, from the HTTP
// goroutine, while the rip goroutine is writing exactly those fields. The
// writers hold the job's mutex; the readers do not, which is a data race
// whichever side is holding what.
//
// Job.Snapshot exists precisely to answer this and was called only from tests.
//
// This test fails under -race before the fix and passes after. Without -race it
// proves nothing, which is the point: nothing in the suite exercised an HTTP
// handler and a running rip at the same time, so the race was invisible.
func TestActivityHandlerDoesNotRaceWithARunningRip(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "RACE_DISC"}, nil)
	mgr.PollOnce(context.Background())

	exec := &progressingRipExecutor{stop: make(chan struct{})}
	engine := ripper.NewEngine(exec)

	srv, _ := setupDashboardServer(t, mgr)
	srv.ripEngine = engine
	srv.echo.GET("/activity/state", srv.handleActivityState)

	tmp := t.TempDir()
	job := ripper.NewJob(0, 0, "RACE_DISC", filepath.Join(tmp, "out"))
	job.TitleName = "Feature"
	job.TrackMetadata = ripper.TrackMetadata{SizeHuman: "20.0 GB", Duration: "1:52:00"}
	done := make(chan struct{})
	job.OnComplete = func(_ *ripper.Job, _ error) error { close(done); return nil }
	if err := os.MkdirAll(job.OutputDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := engine.Submit(job); err != nil {
		t.Fatalf("Submit: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&exec.started) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the rip to start")
		}
		time.Sleep(time.Millisecond)
	}

	// Hammer the page while the rip mutates the job underneath it.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 25; n++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/activity/state", nil)
				req.Header.Set("Accept", "application/json")
				srv.echo.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("activity returned %d", rec.Code)
					return
				}
			}
		}()
	}
	wg.Wait()

	close(exec.stop)
	<-done
}
