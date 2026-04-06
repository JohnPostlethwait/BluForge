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
		"BLUFORGE_GITHUB_CLIENT_ID",
		"BLUFORGE_GITHUB_CLIENT_SECRET",
		"BLUFORGE_DUPLICATE_ACTION",
		"BLUFORGE_TMDB_API_KEY",
		"MAKEMKV_KEY",
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
	if cfg.GitHubClientID != "" {
		t.Errorf("GitHubClientID: want empty, got %q", cfg.GitHubClientID)
	}
	if cfg.GitHubClientSecret != "" {
		t.Errorf("GitHubClientSecret: want empty, got %q", cfg.GitHubClientSecret)
	}
	if cfg.DuplicateAction != "skip" {
		t.Errorf("DuplicateAction: want skip, got %s", cfg.DuplicateAction)
	}
	if cfg.MakeMKVKey != "" {
		t.Errorf("MakeMKVKey: want empty, got %q", cfg.MakeMKVKey)
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

// TestSave_RoundTrip verifies that Save writes a config that Load reads back
// with identical values.
func TestSave_RoundTrip(t *testing.T) {
	cfg := AppConfig{
		Port:               8080,
		OutputDir:          "/custom/output",
		AutoRip:            true,
		MinTitleLength:     90,
		PollInterval:       10,
		GitHubClientID:     "gh-id",
		GitHubClientSecret: "gh-secret",
		DuplicateAction:    "overwrite",
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(cfg, configPath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	clearEnv(t)

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Port != cfg.Port {
		t.Errorf("Port: want %d, got %d", cfg.Port, loaded.Port)
	}
	if loaded.OutputDir != cfg.OutputDir {
		t.Errorf("OutputDir: want %q, got %q", cfg.OutputDir, loaded.OutputDir)
	}
	if loaded.AutoRip != cfg.AutoRip {
		t.Errorf("AutoRip: want %v, got %v", cfg.AutoRip, loaded.AutoRip)
	}
	if loaded.MinTitleLength != cfg.MinTitleLength {
		t.Errorf("MinTitleLength: want %d, got %d", cfg.MinTitleLength, loaded.MinTitleLength)
	}
	if loaded.PollInterval != cfg.PollInterval {
		t.Errorf("PollInterval: want %d, got %d", cfg.PollInterval, loaded.PollInterval)
	}
	if loaded.GitHubClientID != cfg.GitHubClientID {
		t.Errorf("GitHubClientID: want %q, got %q", cfg.GitHubClientID, loaded.GitHubClientID)
	}
	if loaded.GitHubClientSecret != cfg.GitHubClientSecret {
		t.Errorf("GitHubClientSecret: want %q, got %q", cfg.GitHubClientSecret, loaded.GitHubClientSecret)
	}
	if loaded.DuplicateAction != cfg.DuplicateAction {
		t.Errorf("DuplicateAction: want %q, got %q", cfg.DuplicateAction, loaded.DuplicateAction)
	}
}

// TestSave_InvalidPath verifies that Save returns an error for an invalid path.
func TestSave_InvalidPath(t *testing.T) {
	err := Save(AppConfig{}, "/nonexistent/dir/config.yaml")
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}

func TestLoadMakeMKVKeyFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("MAKEMKV_KEY", "T-abc123def456")

	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.yaml"))
	if err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}

	if cfg.MakeMKVKey != "T-abc123def456" {
		t.Errorf("MakeMKVKey: want %q, got %q", "T-abc123def456", cfg.MakeMKVKey)
	}
}

func TestLoadFromEnv_TMDBApiKey(t *testing.T) {
	clearEnv(t)
	t.Setenv("BLUFORGE_TMDB_API_KEY", "test-tmdb-key-123")
	cfg := LoadFromEnv()
	if cfg.TMDBApiKey != "test-tmdb-key-123" {
		t.Errorf("TMDBApiKey: want %q, got %q", "test-tmdb-key-123", cfg.TMDBApiKey)
	}
}

func TestSave_RoundTrip_MakeMKVKey(t *testing.T) {
	cfg := AppConfig{
		Port:            9160,
		OutputDir:       "/output",
		MakeMKVKey:      "T-abc123def456",
		PollInterval:    5,
		DuplicateAction: "skip",
	}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(cfg, configPath); err != nil {
		t.Fatalf("Save: %v", err)
	}

	clearEnv(t)

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.MakeMKVKey != cfg.MakeMKVKey {
		t.Errorf("MakeMKVKey: want %q, got %q", cfg.MakeMKVKey, loaded.MakeMKVKey)
	}
}
