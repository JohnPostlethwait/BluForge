package db

import (
	"database/sql"
	"fmt"
	"time"
)

// DiscMapping represents a row in the disc_mappings table.
type DiscMapping struct {
	ID          int64
	DiscKey     string
	DiscName    string
	MediaItemID string
	ReleaseID   string
	DiscID      string
	MediaTitle  string
	MediaYear   string
	MediaType   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// SaveMapping inserts or replaces a disc mapping (upsert on disc_key conflict).
func (s *Store) SaveMapping(m DiscMapping) error {
	const q = `
		INSERT INTO disc_mappings
			(disc_key, disc_name, media_item_id, release_id, disc_id, media_title, media_year, media_type)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(disc_key) DO UPDATE SET
			disc_name    = excluded.disc_name,
			media_item_id = excluded.media_item_id,
			release_id   = excluded.release_id,
			disc_id      = excluded.disc_id,
			media_title  = excluded.media_title,
			media_year   = excluded.media_year,
			media_type   = excluded.media_type,
			updated_at   = CURRENT_TIMESTAMP`

	_, err := s.db.Exec(q,
		m.DiscKey, m.DiscName, m.MediaItemID, m.ReleaseID, m.DiscID,
		m.MediaTitle, m.MediaYear, m.MediaType,
	)
	if err != nil {
		return fmt.Errorf("save mapping: %w", err)
	}

	return nil
}

// GetMapping retrieves a disc mapping by disc_key.
// Returns nil, nil if the mapping does not exist.
func (s *Store) GetMapping(discKey string) (*DiscMapping, error) {
	const q = `
		SELECT id, disc_key, disc_name, media_item_id, release_id, disc_id,
		       media_title, media_year, media_type, created_at, updated_at
		FROM disc_mappings WHERE disc_key = ?`

	row := s.db.QueryRow(q, discKey)
	m, err := scanMapping(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mapping %q: %w", discKey, err)
	}

	return m, nil
}

// DeleteMapping removes a disc mapping by disc_key.
func (s *Store) DeleteMapping(discKey string) error {
	const q = `DELETE FROM disc_mappings WHERE disc_key = ?`

	_, err := s.db.Exec(q, discKey)
	if err != nil {
		return fmt.Errorf("delete mapping %q: %w", discKey, err)
	}

	return nil
}

// ListMappings returns all disc mappings.
func (s *Store) ListMappings() ([]DiscMapping, error) {
	const q = `
		SELECT id, disc_key, disc_name, media_item_id, release_id, disc_id,
		       media_title, media_year, media_type, created_at, updated_at
		FROM disc_mappings
		ORDER BY created_at DESC`

	rows, err := s.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("list mappings: %w", err)
	}
	defer rows.Close()

	var mappings []DiscMapping
	for rows.Next() {
		m, err := scanMapping(rows)
		if err != nil {
			return nil, fmt.Errorf("scan mapping: %w", err)
		}
		mappings = append(mappings, *m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return mappings, nil
}

func scanMapping(s scanner) (*DiscMapping, error) {
	var m DiscMapping
	err := s.Scan(
		&m.ID, &m.DiscKey, &m.DiscName, &m.MediaItemID, &m.ReleaseID, &m.DiscID,
		&m.MediaTitle, &m.MediaYear, &m.MediaType,
		&m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &m, nil
}
