package discdb

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

// Cache provides a SQLite-backed TTL cache for TheDiscDB HTTP responses.
type Cache struct {
	store *db.Store
	ttl   time.Duration
}

// NewCache creates a new Cache backed by store with the given TTL.
func NewCache(store *db.Store, ttl time.Duration) *Cache {
	return &Cache{store: store, ttl: ttl}
}

// Get returns the cached response for key, or nil, nil if not found or expired.
// Expired entries are deleted before returning nil.
func (c *Cache) Get(key string) ([]byte, error) {
	var responseJSON string
	var expiresAt time.Time

	row := c.store.QueryRow(
		`SELECT response_json, expires_at FROM discdb_cache WHERE cache_key = ?`,
		key,
	)
	err := row.Scan(&responseJSON, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache get: %w", err)
	}

	if time.Now().After(expiresAt) {
		if _, delErr := c.store.Exec(`DELETE FROM discdb_cache WHERE cache_key = ?`, key); delErr != nil {
			return nil, fmt.Errorf("cache delete expired: %w", delErr)
		}
		return nil, nil
	}

	return []byte(responseJSON), nil
}

// Set upserts data into the cache for key, expiring at now + ttl.
func (c *Cache) Set(key string, data []byte) error {
	expiresAt := time.Now().Add(c.ttl)
	_, err := c.store.Exec(
		`INSERT INTO discdb_cache (cache_key, response_json, expires_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(cache_key) DO UPDATE SET
		     response_json = excluded.response_json,
		     expires_at    = excluded.expires_at`,
		key,
		string(data),
		expiresAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("cache set: %w", err)
	}
	return nil
}
