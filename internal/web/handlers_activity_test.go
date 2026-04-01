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

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
)

// ---------------------------------------------------------------------------
// Test executors
// ---------------------------------------------------------------------------

// blockingRipExecutor blocks on StartRip until the block channel is closed.
type blockingRipExecutor struct {
	started int32
	block   chan struct{}
}

func (b *blockingRipExecutor) StartRip(_ context.Context, _ int, _ int, outputDir string, onEvent func(makemkv.Event)) error {
	atomic.AddInt32(&b.started, 1)
	<-b.block
	// Write a fake file so orchestrator's OnComplete doesn't fail.
	_ = os.WriteFile(filepath.Join(outputDir, "title.mkv"), []byte("fake"), 0o644)
	if onEvent != nil {
		onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Current: 65536, Total: 65536, Max: 65536}})
	}
	return nil
}

// immediateRipExecutor completes instantly.
type immediateRipExecutor struct{}

func (m *immediateRipExecutor) StartRip(_ context.Context, _ int, _ int, outputDir string, onEvent func(makemkv.Event)) error {
	_ = os.WriteFile(filepath.Join(outputDir, "title.mkv"), []byte("fake"), 0o644)
	if onEvent != nil {
		onEvent(makemkv.Event{Type: "PRGV", Progress: &makemkv.Progress{Current: 65536, Total: 65536, Max: 65536}})
	}
	return nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func setupActivityServer(t *testing.T) (*Server, *db.Store) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	outputDir := filepath.Join(tmpDir, "output")
	os.MkdirAll(outputDir, 0o755)

	executor := &immediateRipExecutor{}
	engine := ripper.NewEngine(executor)
	org := organizer.New()
	orch := workflow.NewOrchestrator(workflow.OrchestratorDeps{
		Store:       store,
		Engine:      engine,
		Organizer:   org,
		OnBroadcast: func(string, string) {},
	})

	cfg := &config.AppConfig{OutputDir: outputDir, DuplicateAction: "overwrite"}
	cfgPath := filepath.Join(tmpDir, "config.yaml")
	config.Save(*cfg, cfgPath)

	srv := &Server{
		echo:          echo.New(),
		cfg:           cfg,
		configPath:    cfgPath,
		store:         store,
		ripEngine:     engine,
		orchestrator:  orch,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	return srv, store
}

// ---------------------------------------------------------------------------
// Cancel tests
// ---------------------------------------------------------------------------

func TestHandleActivityCancel_NilEngine(t *testing.T) {
	srv := &Server{
		echo:          echo.New(),
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/activity/1/cancel", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("1")

	err := srv.handleActivityCancel(c)
	if he, ok := err.(*echo.HTTPError); ok {
		if he.Code != http.StatusServiceUnavailable {
			t.Errorf("expected 503, got %d", he.Code)
		}
	} else {
		t.Fatalf("expected HTTPError, got %v", err)
	}
}

func TestHandleActivityCancel_RemovesQueued(t *testing.T) {
	blocker := &blockingRipExecutor{block: make(chan struct{})}
	engine := ripper.NewEngine(blocker)

	tmpDir := t.TempDir()

	// Job 1 will start running immediately (blocks).
	job1 := ripper.NewJob(0, 0, "Disc1", filepath.Join(tmpDir, "out1"))
	job1.ID = 1
	os.MkdirAll(job1.OutputDir, 0o755)
	engine.Submit(job1)

	// Wait for job1 to start executing.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&blocker.started) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for job1 to start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Job 2 queues behind job1 on the same drive.
	job2 := ripper.NewJob(0, 1, "Disc1", filepath.Join(tmpDir, "out2"))
	job2.ID = 2
	os.MkdirAll(job2.OutputDir, 0o755)
	engine.Submit(job2)

	srv := &Server{
		echo:          echo.New(),
		ripEngine:     engine,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/activity/2/cancel", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("2")

	err := srv.handleActivityCancel(c)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "removed" {
		t.Errorf("expected status=removed, got %q", body["status"])
	}

	// Release the blocker so the goroutine can finish.
	close(blocker.block)
}

func TestHandleActivityCancel_CancelsActive(t *testing.T) {
	blocker := &blockingRipExecutor{block: make(chan struct{})}
	engine := ripper.NewEngine(blocker)

	tmpDir := t.TempDir()
	job := ripper.NewJob(0, 0, "Disc1", filepath.Join(tmpDir, "out"))
	job.ID = 1
	os.MkdirAll(job.OutputDir, 0o755)
	engine.Submit(job)

	// Wait for job to start.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&blocker.started) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for job to start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	srv := &Server{
		echo:          echo.New(),
		ripEngine:     engine,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/activity/1/cancel", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("1")

	err := srv.handleActivityCancel(c)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "cancelled" {
		t.Errorf("expected status=cancelled, got %q", body["status"])
	}

	// Release the blocker so the goroutine can clean up.
	close(blocker.block)
}

func TestHandleActivityCancel_NotFound(t *testing.T) {
	engine := ripper.NewEngine(&immediateRipExecutor{})

	srv := &Server{
		echo:          echo.New(),
		ripEngine:     engine,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/activity/999/cancel", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("999")

	err := srv.handleActivityCancel(c)
	if he, ok := err.(*echo.HTTPError); ok {
		if he.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", he.Code)
		}
	} else {
		t.Fatalf("expected HTTPError, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Retry tests
// ---------------------------------------------------------------------------

func TestHandleActivityRetry_FailedJob(t *testing.T) {
	srv, store := setupActivityServer(t)

	// Create a job and mark it as failed.
	id, err := store.CreateJob(db.RipJob{
		DriveIndex:  0,
		DiscName:    "TestDisc",
		TitleIndex:  0,
		TitleName:   "Title 1",
		ContentType: "movie",
		Status:      "pending",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := store.UpdateJobStatus(id, "failed", 0, "something broke"); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/activity/1/retry", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("1")

	err = srv.handleActivityRetry(c)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["status"] != "retried" {
		t.Errorf("expected status=retried, got %q", body["status"])
	}
}

func TestHandleActivityRetry_NonFailedJob(t *testing.T) {
	srv, store := setupActivityServer(t)

	id, err := store.CreateJob(db.RipJob{
		DriveIndex:  0,
		DiscName:    "TestDisc",
		TitleIndex:  0,
		TitleName:   "Title 1",
		ContentType: "movie",
		Status:      "completed",
	})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := store.UpdateJobStatus(id, "completed", 100, ""); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/activity/1/retry", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("1")

	err = srv.handleActivityRetry(c)
	if he, ok := err.(*echo.HTTPError); ok {
		if he.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", he.Code)
		}
	} else {
		t.Fatalf("expected HTTPError, got %v", err)
	}
}

func TestHandleActivityRetry_NotFound(t *testing.T) {
	srv, _ := setupActivityServer(t)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/activity/999/retry", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("999")

	err := srv.handleActivityRetry(c)
	if he, ok := err.(*echo.HTTPError); ok {
		if he.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", he.Code)
		}
	} else {
		t.Fatalf("expected HTTPError, got %v", err)
	}
}
