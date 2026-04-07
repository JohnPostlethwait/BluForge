package web_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/web"
)

// newTestServer creates a minimal Server wired to a temp config file.
func newTestServer(t *testing.T, cfg config.AppConfig) (*web.Server, string) {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	// Write the initial config to disk so Save has a valid destination.
	if err := config.Save(cfg, cfgPath); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	s := web.NewServer(web.ServerDeps{
		Config:     &cfg,
		ConfigPath: cfgPath,
		SSEHub:     web.NewSSEHub(),
	})
	return s, cfgPath
}

// TestGetConfig_ConcurrentAccess verifies that 100 goroutines can call
// getConfig concurrently without a data race.  Run with -race.
func TestGetConfig_ConcurrentAccess(t *testing.T) {
	cfg := config.AppConfig{
		OutputDir:    "/tmp/output",
		AutoRip:      false,
		PollInterval: 5,
		Port:         9160,
	}
	s, _ := newTestServer(t, cfg)

	var wg sync.WaitGroup
	const goroutines = 100

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			got := s.GetConfig()
			if got.OutputDir == "" {
				t.Errorf("unexpected empty OutputDir")
			}
		}()
	}
	wg.Wait()
}

// TestUpdateConfig_Persists verifies that updateConfig changes both the
// in-memory value returned by getConfig and the on-disk YAML file.
func TestUpdateConfig_Persists(t *testing.T) {
	cfg := config.AppConfig{
		OutputDir:       "/old/output",
		PollInterval:    5,
		Port:            9160,
		DuplicateAction: "skip",
	}
	s, cfgPath := newTestServer(t, cfg)

	newDir := "/new/output"
	if err := s.UpdateConfig(func(c *config.AppConfig) {
		c.OutputDir = newDir
	}); err != nil {
		t.Fatalf("UpdateConfig returned error: %v", err)
	}

	// In-memory check.
	got := s.GetConfig()
	if got.OutputDir != newDir {
		t.Errorf("in-memory OutputDir = %q, want %q", got.OutputDir, newDir)
	}

	// On-disk check.
	loaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}
	if loaded.OutputDir != newDir {
		t.Errorf("on-disk OutputDir = %q, want %q", loaded.OutputDir, newDir)
	}

	// Ensure the file actually exists and is non-empty.
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("config file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Error("config file is empty after save")
	}
}

// TestUpdateConfig_RejectsInvalidConfig verifies that UpdateConfig returns an
// error and does not modify the in-memory config when the mutation would produce
// an invalid configuration (e.g. a relative OutputDir).
func TestUpdateConfig_RejectsInvalidConfig(t *testing.T) {
	cfg := config.AppConfig{
		OutputDir:       "/valid/output",
		PollInterval:    5,
		Port:            9160,
		DuplicateAction: "skip",
	}
	s, _ := newTestServer(t, cfg)

	err := s.UpdateConfig(func(c *config.AppConfig) {
		c.OutputDir = "relative/path" // invalid: not absolute
	})
	if err == nil {
		t.Fatal("expected error for invalid OutputDir, got nil")
	}

	// In-memory config must be unchanged.
	got := s.GetConfig()
	if got.OutputDir != "/valid/output" {
		t.Errorf("OutputDir changed despite validation failure: got %q", got.OutputDir)
	}
}
