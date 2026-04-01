package db

import (
	"testing"
)

func makeTestMapping() DiscMapping {
	return DiscMapping{
		DiscKey:     "disc-key-abc123",
		DiscName:    "The Matrix",
		MediaItemID: "item-001",
		ReleaseID:   "release-001",
		MediaTitle:  "The Matrix",
		MediaYear:   "1999",
		MediaType:   "movie",
	}
}

func TestSaveAndGetMapping(t *testing.T) {
	store := openTestDB(t)

	m := makeTestMapping()
	if err := store.SaveMapping(m); err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}

	got, err := store.GetMapping(m.DiscKey)
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if got == nil {
		t.Fatal("GetMapping returned nil")
	}

	if got.DiscKey != m.DiscKey {
		t.Errorf("DiscKey: want %q, got %q", m.DiscKey, got.DiscKey)
	}
	if got.DiscName != m.DiscName {
		t.Errorf("DiscName: want %q, got %q", m.DiscName, got.DiscName)
	}
	if got.MediaItemID != m.MediaItemID {
		t.Errorf("MediaItemID: want %q, got %q", m.MediaItemID, got.MediaItemID)
	}
	if got.ReleaseID != m.ReleaseID {
		t.Errorf("ReleaseID: want %q, got %q", m.ReleaseID, got.ReleaseID)
	}
	if got.MediaTitle != m.MediaTitle {
		t.Errorf("MediaTitle: want %q, got %q", m.MediaTitle, got.MediaTitle)
	}
	if got.MediaYear != m.MediaYear {
		t.Errorf("MediaYear: want %q, got %q", m.MediaYear, got.MediaYear)
	}
	if got.MediaType != m.MediaType {
		t.Errorf("MediaType: want %q, got %q", m.MediaType, got.MediaType)
	}
}

func TestGetMappingNotFound(t *testing.T) {
	store := openTestDB(t)

	got, err := store.GetMapping("nonexistent-key")
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if got != nil {
		t.Errorf("GetMapping: want nil, got %+v", got)
	}
}

func TestDeleteMapping(t *testing.T) {
	store := openTestDB(t)

	m := makeTestMapping()
	if err := store.SaveMapping(m); err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}

	if err := store.DeleteMapping(m.DiscKey); err != nil {
		t.Fatalf("DeleteMapping: %v", err)
	}

	got, err := store.GetMapping(m.DiscKey)
	if err != nil {
		t.Fatalf("GetMapping after delete: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil after delete, got %+v", got)
	}
}

func TestSaveMappingUpserts(t *testing.T) {
	store := openTestDB(t)

	m := makeTestMapping()
	if err := store.SaveMapping(m); err != nil {
		t.Fatalf("SaveMapping first: %v", err)
	}

	// Update with same key but different values.
	m2 := m
	m2.MediaTitle = "The Matrix Reloaded"
	m2.MediaYear = "2003"
	if err := store.SaveMapping(m2); err != nil {
		t.Fatalf("SaveMapping second: %v", err)
	}

	got, err := store.GetMapping(m.DiscKey)
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if got == nil {
		t.Fatal("GetMapping returned nil")
	}

	if got.MediaTitle != "The Matrix Reloaded" {
		t.Errorf("MediaTitle after upsert: want %q, got %q", "The Matrix Reloaded", got.MediaTitle)
	}
	if got.MediaYear != "2003" {
		t.Errorf("MediaYear after upsert: want %q, got %q", "2003", got.MediaYear)
	}
}

func TestListMappings_ReturnsAll(t *testing.T) {
	store := openTestDB(t)

	m1 := makeTestMapping()
	m1.DiscKey = "disc-key-001"
	m1.DiscName = "The Matrix"
	if err := store.SaveMapping(m1); err != nil {
		t.Fatalf("SaveMapping m1: %v", err)
	}

	m2 := makeTestMapping()
	m2.DiscKey = "disc-key-002"
	m2.DiscName = "Inception"
	if err := store.SaveMapping(m2); err != nil {
		t.Fatalf("SaveMapping m2: %v", err)
	}

	m3 := makeTestMapping()
	m3.DiscKey = "disc-key-003"
	m3.DiscName = "Interstellar"
	if err := store.SaveMapping(m3); err != nil {
		t.Fatalf("SaveMapping m3: %v", err)
	}

	mappings, err := store.ListMappings()
	if err != nil {
		t.Fatalf("ListMappings: %v", err)
	}
	if len(mappings) != 3 {
		t.Errorf("expected 3 mappings, got %d", len(mappings))
	}
}

func TestListMappings_EmptyTable(t *testing.T) {
	store := openTestDB(t)

	mappings, err := store.ListMappings()
	if err != nil {
		t.Fatalf("ListMappings: %v", err)
	}
	if mappings != nil && len(mappings) != 0 {
		t.Errorf("expected nil or empty slice, got %d mappings", len(mappings))
	}
}
