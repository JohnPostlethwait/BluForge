package db

import (
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/johnpostlethwait/bluforge/migrations"
	_ "modernc.org/sqlite"
)

// Store wraps a *sql.DB and provides access to all database operations.
type Store struct {
	db *sql.DB
}

// dsn builds a connection string carrying the pragmas every connection needs.
//
// A plain path is accepted for ":memory:" and for callers that already pass a
// file: URI; anything else is wrapped so the pragmas can be appended.
func dsn(dbPath string) string {
	const pragmas = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	if strings.Contains(dbPath, "?") {
		return dbPath + "&" + pragmas
	}
	return dbPath + "?" + pragmas
}

// Open opens (or creates) the SQLite database at dbPath, enables WAL mode,
// and runs all pending migrations. Use ":memory:" for in-memory databases.
func Open(dbPath string) (*Store, error) {
	// Pragmas go in the DSN so every pooled connection gets them. Setting them
	// with db.Exec applies to whichever single connection happened to serve the
	// call: the pool opens more on demand, and those got a busy timeout of zero.
	// A rip writing its status while the activity page queried was then enough
	// to produce "database is locked", losing the update outright.
	db, err := sql.Open("sqlite", dsn(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
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
			// ALTER TABLE ADD COLUMN is not idempotent in SQLite;
			// skip "duplicate column name" errors on re-run.
			if strings.Contains(err.Error(), "duplicate column name") {
				continue
			}
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
