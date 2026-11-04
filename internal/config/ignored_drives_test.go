package config

import (
	"path/filepath"
	"testing"
)

func TestSave_RoundTrip_IgnoredDrives(t *testing.T) {
	cfg := defaults()
	cfg.IgnoredDrives = []string{"BD-RE ASUS BW-16D1HT 3.10 KLFO82D3028"}

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := Save(cfg, configPath); err != nil {
		t.Fatalf("Save: %v", err)
	}
	clearEnv(t)

	loaded, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.IgnoredDrives) != 1 || loaded.IgnoredDrives[0] != cfg.IgnoredDrives[0] {
		t.Errorf("IgnoredDrives: want %v, got %v", cfg.IgnoredDrives, loaded.IgnoredDrives)
	}
}
