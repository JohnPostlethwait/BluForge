package config

import (
	"os"
	"path/filepath"
	"testing"
)

// clearEnv removes all BLUFORGE_* environment variables so tests start clean.
func clearEnv(t *testing.T) {
	t.Helper()
	vars := []string{
		"BLUFORGE_PORT",
		"BLUFORGE_OUTPUT_DIR",
		"BLUFORGE_AUTO_RIP",
		"BLUFORGE_MIN_TITLE_LENGTH",
		"BLUFORGE_POLL_INTERVAL",
		"BLUFORGE_MOVIE_TEMPLATE",
		"BLUFORGE_SERIES_TEMPLATE",
		"BLUFORGE_GITHUB_CLIENT_ID",
		"BLUFORGE_GITHUB_CLIENT_SECRET",
		"BLUFORGE_DUPLICATE_ACTION",
	}
	for _, v := range vars {
		t.Setenv(v, "") // registers cleanup; set empty so Getenv returns ""
		os.Unsetenv(v)
	}
}

// TestLoadReturnsDefaults verifies all default values when no env vars or
// config file are present.
func TestLoadReturnsDefaults(t *testing.T) {
	clearEnv(t)

	// Use a path that does not exist so no file override occurs.
	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}

	if cfg.Port != 9160 {
		t.Errorf("Port: want 9160, got %d", cfg.Port)
	}
	if cfg.OutputDir != "/output" {
		t.Errorf("OutputDir: want /output, got %s", cfg.OutputDir)
	}
	if cfg.AutoRip != false {
		t.Errorf("AutoRip: want false, got %v", cfg.AutoRip)
	}
	if cfg.MinTitleLength != 120 {
		t.Errorf("MinTitleLength: want 120, got %d", cfg.MinTitleLength)
	}
	if cfg.PollInterval != 5 {
		t.Errorf("PollInterval: want 5, got %d", cfg.PollInterval)
	}
	want := "Movies/{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}})"
	if cfg.MovieTemplate != want {
		t.Errorf("MovieTemplate: want %q, got %q", want, cfg.MovieTemplate)
	}
	wantSeries := "TV/{{.Show}}/Season {{.Season}}/{{.Show}} - S{{.Season}}E{{.Episode}} - {{.EpisodeTitle}}"
	if cfg.SeriesTemplate != wantSeries {
		t.Errorf("SeriesTemplate: want %q, got %q", wantSeries, cfg.SeriesTemplate)
	}
	if cfg.GitHubClientID != "" {
		t.Errorf("GitHubClientID: want empty, got %q", cfg.GitHubClientID)
	}
	if cfg.GitHubClientSecret != "" {
		t.Errorf("GitHubClientSecret: want empty, got %q", cfg.GitHubClientSecret)
	}
	if cfg.DuplicateAction != "skip" {
		t.Errorf("DuplicateAction: want skip, got %s", cfg.DuplicateAction)
	}
}

// TestLoadRespectsEnvVars verifies that BLUFORGE_* env vars override defaults.
func TestLoadRespectsEnvVars(t *testing.T) {
	clearEnv(t)

	t.Setenv("BLUFORGE_PORT", "8080")
	t.Setenv("BLUFORGE_AUTO_RIP", "true")
	t.Setenv("BLUFORGE_MIN_TITLE_LENGTH", "90")

	// No config file.
	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("Port: want 8080, got %d", cfg.Port)
	}
	if cfg.AutoRip != true {
		t.Errorf("AutoRip: want true, got %v", cfg.AutoRip)
	}
	if cfg.MinTitleLength != 90 {
		t.Errorf("MinTitleLength: want 90, got %d", cfg.MinTitleLength)
	}
	// Values not set via env should still be default.
	if cfg.OutputDir != "/output" {
		t.Errorf("OutputDir: want /output, got %s", cfg.OutputDir)
	}
}

// TestLoadFromFileOverridesEnv verifies that a YAML config file wins over env vars.
func TestLoadFromFileOverridesEnv(t *testing.T) {
	clearEnv(t)

	// Set env var to a specific port.
	t.Setenv("BLUFORGE_PORT", "8080")

	// Write a YAML file with a different port value.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	content := "port: 7777\n"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}

	// File value should win over env var.
	if cfg.Port != 7777 {
		t.Errorf("Port: want 7777 (from file), got %d", cfg.Port)
	}
}
