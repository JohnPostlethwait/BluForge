package db

import (
	"database/sql"
	"testing"
)

func openTestDB(t *testing.T) *Store {
	t.Helper()

	// Use :memory: for fast, isolated unit tests that need no on-disk persistence.
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() { store.Close() })

	return store
}

func TestOpenCreatesSchema(t *testing.T) {
	store := openTestDB(t)

	tables := []string{"rip_jobs", "disc_mappings", "discdb_cache", "settings", "contributions"}
	for _, table := range tables {
		var name string
		err := store.db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err == sql.ErrNoRows {
			t.Errorf("table %q does not exist", table)
		} else if err != nil {
			t.Errorf("query table %q: %v", table, err)
		}
	}
}
