package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// setupDashboardServer creates a Server with a real DB, suitable for dashboard
// handler tests.
func setupDashboardServer(t *testing.T, mgr *drivemanager.Manager) (*Server, *db.Store) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := &config.AppConfig{OutputDir: tmpDir}

	srv := &Server{
		echo:          echo.New(),
		cfg:           cfg,
		store:         store,
		driveMgr:      mgr,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	return srv, store
}

func TestHandleDashboard_NoJobs(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	srv, _ := setupDashboardServer(t, mgr)
	srv.echo.GET("/", srv.handleDashboard)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Dashboard") && !strings.Contains(body, "dashboard") {
		t.Error("response should contain 'Dashboard' or 'dashboard'")
	}
}

func TestHandleDashboard_WithActiveRip(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "TEST_DISC"}, nil)
	mgr.PollOnce(context.Background())

	blocker := &blockingRipExecutor{block: make(chan struct{})}
	engine := ripper.NewEngine(blocker)

	srv, _ := setupDashboardServer(t, mgr)
	srv.ripEngine = engine
	srv.echo.GET("/", srv.handleDashboard)

	tmpDir := t.TempDir()
	job := ripper.NewJob(0, 0, "TEST_DISC", filepath.Join(tmpDir, "out"))
	jobDone := make(chan struct{})
	job.OnComplete = func(_ *ripper.Job, _ error) { close(jobDone) }

	if err := os.MkdirAll(job.OutputDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	engine.Submit(job)

	// Wait for the rip to start so it appears in ActiveJobs().
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&blocker.started) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for job to start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"activeCount":1`) {
		t.Error("expected activeCount:1 in store JSON")
	}

	close(blocker.block)
	<-jobDone
}

func TestHandleDashboard_NilEngine(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	srv, _ := setupDashboardServer(t, mgr)
	srv.ripEngine = nil
	srv.echo.GET("/", srv.handleDashboard)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with nil engine, got %d: %s", rec.Code, rec.Body.String())
	}
}
