package web

import (
	"context"
	"encoding/json"
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

func (b *blockingRipExecutor) StartRip(_ context.Context, _ int, _ int, outputDir string, onEvent func(makemkv.Event), _ *makemkv.SelectionOpts) error {
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

func (m *immediateRipExecutor) StartRip(_ context.Context, _ int, _ int, outputDir string, onEvent func(makemkv.Event), _ *makemkv.SelectionOpts) error {
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
	// Use a file-based DB (rather than :memory:) so these handler integration
	// tests exercise real filesystem I/O and run closer to production conditions.
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
	job1Done := make(chan struct{})
	job1 := ripper.NewJob(0, 0, "Disc1", filepath.Join(tmpDir, "out1"))
	job1.ID = 1
	job1.OnComplete = func(_ *ripper.Job, _ error) { close(job1Done) }
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

	// Release the blocker and wait for job1 to fully finish before the test
	// exits, so t.TempDir() cleanup doesn't race with the engine goroutine
	// writing title.mkv into out1.
	close(blocker.block)
	<-job1Done
}

func TestHandleActivityCancel_CancelsActive(t *testing.T) {
	blocker := &blockingRipExecutor{block: make(chan struct{})}
	engine := ripper.NewEngine(blocker)

	tmpDir := t.TempDir()
	jobDone := make(chan struct{})
	job := ripper.NewJob(0, 0, "Disc1", filepath.Join(tmpDir, "out"))
	job.ID = 1
	job.OnComplete = func(_ *ripper.Job, _ error) { close(jobDone) }
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

	// Release the blocker and wait for the engine goroutine to finish before
	// the test exits, so t.TempDir() cleanup doesn't race with the goroutine
	// writing title.mkv into the output directory.
	close(blocker.block)
	<-jobDone
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

func TestHandleActivityClearFiltered_StatusFilter(t *testing.T) {
	srv, store := setupActivityServer(t)

	completed := db.RipJob{DiscName: "Disc A", TitleName: "Title A", Status: "completed"}
	idC, err := store.CreateJob(completed)
	if err != nil {
		t.Fatalf("CreateJob completed: %v", err)
	}

	failed := db.RipJob{DiscName: "Disc B", TitleName: "Title B", Status: "failed"}
	idF, err := store.CreateJob(failed)
	if err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	e := echo.New()
	body := `{"search":"","status":"failed"}`
	req := httptest.NewRequest(http.MethodPost, "/activity/clear-filtered",
		strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := srv.handleActivityClearFiltered(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var respBody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if respBody["status"] != "ok" {
		t.Errorf(`expected status="ok", got %q`, respBody["status"])
	}

	got, err := store.GetJob(idC)
	if err != nil {
		t.Fatalf("GetJob completed: %v", err)
	}
	if got == nil {
		t.Error("completed job should still exist but was deleted")
	}

	got, err = store.GetJob(idF)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if got != nil {
		t.Error("failed job should have been deleted but still exists")
	}
}

func TestHandleActivityClearFiltered_SearchFilter(t *testing.T) {
	srv, store := setupActivityServer(t)

	batman := db.RipJob{DiscName: "Batman Begins", TitleName: "Feature", Status: "completed"}
	idB, err := store.CreateJob(batman)
	if err != nil {
		t.Fatalf("CreateJob batman: %v", err)
	}

	other := db.RipJob{DiscName: "Superman", TitleName: "Feature", Status: "completed"}
	idO, err := store.CreateJob(other)
	if err != nil {
		t.Fatalf("CreateJob other: %v", err)
	}

	e := echo.New()
	body := `{"search":"batman","status":""}`
	req := httptest.NewRequest(http.MethodPost, "/activity/clear-filtered",
		strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := srv.handleActivityClearFiltered(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var respBody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if respBody["status"] != "ok" {
		t.Errorf(`expected status="ok", got %q`, respBody["status"])
	}

	got, err := store.GetJob(idB)
	if err != nil {
		t.Fatalf("GetJob batman: %v", err)
	}
	if got != nil {
		t.Error("batman job should have been deleted but still exists")
	}

	got, err = store.GetJob(idO)
	if err != nil {
		t.Fatalf("GetJob other: %v", err)
	}
	if got == nil {
		t.Error("other job should still exist but was deleted")
	}
}

func TestHandleActivityClearFiltered_BothFilters(t *testing.T) {
	srv, store := setupActivityServer(t)

	j1 := db.RipJob{DiscName: "Batman", TitleName: "Feature", Status: "completed"}
	id1, err := store.CreateJob(j1)
	if err != nil {
		t.Fatalf("CreateJob j1: %v", err)
	}

	j2 := db.RipJob{DiscName: "Batman Forever", TitleName: "Feature", Status: "failed"}
	id2, err := store.CreateJob(j2)
	if err != nil {
		t.Fatalf("CreateJob j2: %v", err)
	}

	j3 := db.RipJob{DiscName: "Superman", TitleName: "Feature", Status: "completed"}
	id3, err := store.CreateJob(j3)
	if err != nil {
		t.Fatalf("CreateJob j3: %v", err)
	}

	e := echo.New()
	body := `{"search":"batman","status":"completed"}`
	req := httptest.NewRequest(http.MethodPost, "/activity/clear-filtered",
		strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := srv.handleActivityClearFiltered(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var respBody map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if respBody["status"] != "ok" {
		t.Errorf(`expected status="ok", got %q`, respBody["status"])
	}

	got, err := store.GetJob(id1)
	if err != nil {
		t.Fatalf("GetJob j1: %v", err)
	}
	if got != nil {
		t.Error("j1 should have been deleted but still exists")
	}

	got, err = store.GetJob(id2)
	if err != nil {
		t.Fatalf("GetJob j2: %v", err)
	}
	if got == nil {
		t.Error("j2 should still exist but was deleted")
	}

	got, err = store.GetJob(id3)
	if err != nil {
		t.Fatalf("GetJob j3: %v", err)
	}
	if got == nil {
		t.Error("j3 should still exist but was deleted")
	}
}

func TestHandleActivityClearFiltered_ActiveJobNotDeleted(t *testing.T) {
	blocker := &blockingRipExecutor{block: make(chan struct{})}
	engine := ripper.NewEngine(blocker)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	outputDir := filepath.Join(tmpDir, "output")
	os.MkdirAll(outputDir, 0o755)

	// Submit a job to the engine so it becomes active.
	activeJob := ripper.NewJob(0, 0, "Batman Begins", filepath.Join(tmpDir, "out"))
	activeJob.ID = 99
	os.MkdirAll(activeJob.OutputDir, 0o755)
	engine.Submit(activeJob)

	// Wait for job to start.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&blocker.started) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for job to start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Insert a DB record with the same ID to simulate the active job in the DB.
	dbJob := db.RipJob{DiscName: "Batman Begins", TitleName: "Feature", Status: "ripping"}
	id, err := store.CreateJob(dbJob)
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	// Override the engine job ID to match the DB record.
	activeJob.ID = id

	srv := &Server{
		echo:          echo.New(),
		store:         store,
		ripEngine:     engine,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}

	e := echo.New()
	body := `{"search":"batman","status":""}`
	req := httptest.NewRequest(http.MethodPost, "/activity/clear-filtered",
		strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := srv.handleActivityClearFiltered(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// The active job's DB record should still exist (it was excluded).
	got, err := store.GetJob(id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got == nil {
		t.Error("active job should not have been deleted but was")
	}

	// Release the blocker to allow cleanup.
	close(blocker.block)
}

func TestHandleActivity_ActiveJobIncludesStartedAt(t *testing.T) {
	blocker := &blockingRipExecutor{block: make(chan struct{})}
	engine := ripper.NewEngine(blocker)

	tmpDir := t.TempDir()
	job := ripper.NewJob(0, 0, "TestDisc", filepath.Join(tmpDir, "out"))
	job.ID = 1
	os.MkdirAll(job.OutputDir, 0o755)
	engine.Submit(job)

	// Wait for job to start so StartedAt is set.
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&blocker.started) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for job to start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv := &Server{
		echo:          echo.New(),
		ripEngine:     engine,
		store:         store,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/activity", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := srv.handleActivity(c); err != nil {
		t.Fatalf("handleActivity: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"startedAt"`) {
		t.Error(`response body does not contain "startedAt" — field not exposed in activityJobJSON`)
	}

	const startedAtPrefix = `"startedAt":"`
	idx := strings.Index(body, startedAtPrefix)
	if idx == -1 {
		t.Fatal(`"startedAt" key not found in body`)
	}
	val := body[idx+len(startedAtPrefix):]
	val = val[:strings.IndexByte(val, '"')]
	if _, err := time.Parse(time.RFC3339, val); err != nil {
		t.Errorf("startedAt value %q is not a valid RFC3339 timestamp: %v", val, err)
	}

	close(blocker.block)
}
