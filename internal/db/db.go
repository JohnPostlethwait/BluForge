package db

import (
	"database/sql"
	"fmt"
	"io/fs"
	"sort"

	"github.com/johnpostlethwait/bluforge/migrations"
	_ "modernc.org/sqlite"
)

// Store wraps a *sql.DB and provides access to all database operations.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at dbPath, enables WAL mode,
// and runs all pending migrations. Use ":memory:" for in-memory databases.
func Open(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	store := &Store{db: db}
	if err := store.runMigrations(); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return store, nil
}

// runMigrations reads and executes all *.sql files from the embedded migrations FS.
func (s *Store) runMigrations() error {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	// Sort to ensure deterministic execution order.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		data, err := fs.ReadFile(migrations.FS, entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		if _, err := s.db.Exec(string(data)); err != nil {
			return fmt.Errorf("execute migration %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// QueryRow executes a query expected to return at most one row.
// Exposed for use by the cache package.
func (s *Store) QueryRow(query string, args ...any) *sql.Row {
	return s.db.QueryRow(query, args...)
}

// Exec executes a query that does not return rows.
// Exposed for use by the cache package.
func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.db.Exec(query, args...)
}
