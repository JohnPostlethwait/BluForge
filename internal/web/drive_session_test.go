package web

import (
	"testing"
)

func TestDriveSessionStore_SetAndGet(t *testing.T) {
	store := NewDriveSessionStore()

	session := &DriveSession{
		MediaItemID: "1",
		ReleaseID:   "10",
		MediaTitle:  "Seinfeld",
		MediaYear:   "1989",
		MediaType:   "Series",
	}

	store.Set(0, session)

	got := store.Get(0)
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.MediaTitle != "Seinfeld" {
		t.Errorf("MediaTitle: got %q, want %q", got.MediaTitle, "Seinfeld")
	}
	if got.ReleaseID != "10" {
		t.Errorf("ReleaseID: got %q, want %q", got.ReleaseID, "10")
	}
}

func TestDriveSessionStore_GetMissing(t *testing.T) {
	store := NewDriveSessionStore()

	got := store.Get(99)
	if got != nil {
		t.Errorf("expected nil for missing drive, got %+v", got)
	}
}

func TestDriveSessionStore_Clear(t *testing.T) {
	store := NewDriveSessionStore()

	store.Set(0, &DriveSession{MediaTitle: "Test"})
	store.Clear(0)

	got := store.Get(0)
	if got != nil {
		t.Errorf("expected nil after clear, got %+v", got)
	}
}

func TestDriveSessionStore_SetSearchResults(t *testing.T) {
	store := NewDriveSessionStore()

	store.Set(0, &DriveSession{MediaTitle: "Seinfeld"})

	results := []SearchResultJSON{
		{MediaTitle: "Seinfeld", MediaYear: 1989, ReleaseID: "10"},
		{MediaTitle: "Seinfeld", MediaYear: 1989, ReleaseID: "11"},
	}
	store.SetSearchResults(0, results)

	got := store.Get(0)
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if len(got.SearchResults) != 2 {
		t.Errorf("SearchResults length: got %d, want 2", len(got.SearchResults))
	}
}

func TestDriveSessionStore_SetSearchResults_NoSession(t *testing.T) {
	store := NewDriveSessionStore()

	results := []SearchResultJSON{
		{MediaTitle: "Test", ReleaseID: "1"},
	}
	store.SetSearchResults(0, results)

	got := store.Get(0)
	if got == nil {
		t.Fatal("expected session created for search results, got nil")
	}
	if len(got.SearchResults) != 1 {
		t.Errorf("SearchResults length: got %d, want 1", len(got.SearchResults))
	}
}
