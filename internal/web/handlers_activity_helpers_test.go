package web

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

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
