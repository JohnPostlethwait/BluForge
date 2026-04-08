package db

import (
	"database/sql"
	"fmt"
	"time"
)

// Contribution represents a row in the contributions table.
type Contribution struct {
	ID          int64
	DiscKey     string
	DiscName    string
	RawOutput   string
	ScanJSON    string
	Status      string
	PRURL       string
	TmdbID      string
	ReleaseInfo string
	TitleLabels      string
	ContributionType string // "add" or "update"
	MatchInfo        string // JSON; only populated for "update" type
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// SaveContribution inserts a new contribution and returns the generated ID.
// Returns an error if a contribution with the same disc_key already exists.
func (s *Store) SaveContribution(c Contribution) (int64, error) {
	const q = `
		INSERT INTO contributions
			(disc_key, disc_name, raw_output, scan_json, contribution_type, match_info, title_labels)
		VALUES (?, ?, ?, ?, ?, ?, ?)`

	res, err := s.db.Exec(q, c.DiscKey, c.DiscName, c.RawOutput, c.ScanJSON, c.ContributionType, c.MatchInfo, c.TitleLabels)
	if err != nil {
		return 0, fmt.Errorf("save contribution: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("save contribution last insert id: %w", err)
	}

	return id, nil
}

// GetContribution retrieves a contribution by ID.
// Returns nil, nil if the contribution does not exist.
func (s *Store) GetContribution(id int64) (*Contribution, error) {
	const q = `
		SELECT id, disc_key, disc_name, raw_output, scan_json, status, pr_url,
		       tmdb_id, release_info, title_labels, contribution_type, match_info,
		       created_at, updated_at
		FROM contributions WHERE id = ?`

	row := s.db.QueryRow(q, id)
	c, err := scanContribution(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get contribution %d: %w", id, err)
	}

	return c, nil
}

// GetContributionByDiscKey retrieves a contribution by disc_key.
// Returns nil, nil if no contribution with that key exists.
func (s *Store) GetContributionByDiscKey(discKey string) (*Contribution, error) {
	const q = `
		SELECT id, disc_key, disc_name, raw_output, scan_json, status, pr_url,
		       tmdb_id, release_info, title_labels, contribution_type, match_info,
		       created_at, updated_at
		FROM contributions WHERE disc_key = ?`

	row := s.db.QueryRow(q, discKey)
	c, err := scanContribution(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get contribution by disc_key %q: %w", discKey, err)
	}

	return c, nil
}

// ListContributions returns contributions filtered by status, ordered by created_at DESC.
// If status is empty, all contributions are returned.
func (s *Store) ListContributions(status string) ([]Contribution, error) {
	var (
		q    string
		args []any
	)

	if status == "" {
		q = `
			SELECT id, disc_key, disc_name, raw_output, scan_json, status, pr_url,
			       tmdb_id, release_info, title_labels, contribution_type, match_info,
			       created_at, updated_at
			FROM contributions
			ORDER BY created_at DESC`
	} else {
		q = `
			SELECT id, disc_key, disc_name, raw_output, scan_json, status, pr_url,
			       tmdb_id, release_info, title_labels, contribution_type, match_info,
			       created_at, updated_at
			FROM contributions
			WHERE status = ?
			ORDER BY created_at DESC`
		args = []any{status}
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list contributions: %w", err)
	}
	defer rows.Close()

	var contributions []Contribution
	for rows.Next() {
		c, err := scanContribution(rows)
		if err != nil {
			return nil, fmt.Errorf("scan contribution: %w", err)
		}
		contributions = append(contributions, *c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return contributions, nil
}

// UpdateContributionDraft updates the tmdb_id, release_info, and title_labels fields.
func (s *Store) UpdateContributionDraft(id int64, tmdbID, releaseInfo, titleLabels string) error {
	const q = `
		UPDATE contributions
		SET tmdb_id = ?, release_info = ?, title_labels = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`

	_, err := s.db.Exec(q, tmdbID, releaseInfo, titleLabels, id)
	if err != nil {
		return fmt.Errorf("update contribution draft %d: %w", id, err)
	}

	return nil
}

// UpdateContributionMatchInfo updates the match_info JSON for a contribution.
func (s *Store) UpdateContributionMatchInfo(id int64, matchInfo string) error {
	const q = `
		UPDATE contributions
		SET match_info = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`
	_, err := s.db.Exec(q, matchInfo, id)
	if err != nil {
		return fmt.Errorf("update contribution match_info %d: %w", id, err)
	}
	return nil
}

// UpdateContributionStatus updates the status and pr_url fields.
func (s *Store) UpdateContributionStatus(id int64, status, prURL string) error {
	const q = `
		UPDATE contributions
		SET status = ?, pr_url = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`

	_, err := s.db.Exec(q, status, prURL, id)
	if err != nil {
		return fmt.Errorf("update contribution status %d: %w", id, err)
	}

	return nil
}

// DeleteContribution removes a contribution by ID.
func (s *Store) DeleteContribution(id int64) error {
	const q = `DELETE FROM contributions WHERE id = ?`
	_, err := s.db.Exec(q, id)
	if err != nil {
		return fmt.Errorf("delete contribution %d: %w", id, err)
	}
	return nil
}

func scanContribution(s scanner) (*Contribution, error) {
	var c Contribution
	err := s.Scan(
		&c.ID, &c.DiscKey, &c.DiscName, &c.RawOutput, &c.ScanJSON,
		&c.Status, &c.PRURL, &c.TmdbID, &c.ReleaseInfo, &c.TitleLabels,
		&c.ContributionType, &c.MatchInfo,
		&c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &c, nil
}
