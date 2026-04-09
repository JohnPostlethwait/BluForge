package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

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
