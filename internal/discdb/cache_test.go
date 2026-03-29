package discdb

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

// testStore opens an in-memory SQLite database with all migrations applied.
func testStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestCacheSetAndGet(t *testing.T) {
	store := testStore(t)
	cache := NewCache(store, time.Hour)

	payload := map[string]string{"title": "The Matrix", "year": "1999"}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	if err := cache.Set("test-key", data); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := cache.Get("test-key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil result, got nil")
	}

	var result map[string]string
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["title"] != payload["title"] {
		t.Errorf("title: got %q, want %q", result["title"], payload["title"])
	}
	if result["year"] != payload["year"] {
		t.Errorf("year: got %q, want %q", result["year"], payload["year"])
	}
}

func TestCacheExpiry(t *testing.T) {
	store := testStore(t)
	cache := NewCache(store, time.Millisecond)

	data := []byte(`{"msg":"expires fast"}`)
	if err := cache.Set("expiring-key", data); err != nil {
		t.Fatalf("Set: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	got, err := cache.Get("expiring-key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for expired key, got %q", got)
	}
}

func TestCacheMiss(t *testing.T) {
	store := testStore(t)
	cache := NewCache(store, time.Hour)

	got, err := cache.Get("nonexistent-key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing key, got %q", got)
	}
}
