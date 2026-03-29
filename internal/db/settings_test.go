package db

import (
	"testing"
)

func TestSetAndGetSetting(t *testing.T) {
	store := openTestDB(t)

	if err := store.SetSetting("theme", "dark"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	got, err := store.GetSetting("theme")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != "dark" {
		t.Errorf("GetSetting: want %q, got %q", "dark", got)
	}
}

func TestGetSettingDefault(t *testing.T) {
	store := openTestDB(t)

	got, err := store.GetSettingDefault("missing-key", "fallback-value")
	if err != nil {
		t.Fatalf("GetSettingDefault: %v", err)
	}
	if got != "fallback-value" {
		t.Errorf("GetSettingDefault: want %q, got %q", "fallback-value", got)
	}
}

func TestSetSettingUpserts(t *testing.T) {
	store := openTestDB(t)

	if err := store.SetSetting("output_dir", "/old/path"); err != nil {
		t.Fatalf("SetSetting first: %v", err)
	}
	if err := store.SetSetting("output_dir", "/new/path"); err != nil {
		t.Fatalf("SetSetting second: %v", err)
	}

	got, err := store.GetSetting("output_dir")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != "/new/path" {
		t.Errorf("GetSetting after upsert: want %q, got %q", "/new/path", got)
	}
}
