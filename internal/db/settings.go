package db

import (
	"database/sql"
	"fmt"
)

// SetSetting upserts a key/value pair in the settings table.
func (s *Store) SetSetting(key, value string) error {
	const q = `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`

	_, err := s.db.Exec(q, key, value)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}

	return nil
}

// GetSetting retrieves the value for the given key.
// Returns "", nil if the key does not exist.
func (s *Store) GetSetting(key string) (string, error) {
	const q = `SELECT value FROM settings WHERE key = ?`

	var value string
	err := s.db.QueryRow(q, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}

	return value, nil
}

// GetSettingDefault retrieves the value for the given key.
// Returns fallback if the key does not exist.
func (s *Store) GetSettingDefault(key, fallback string) (string, error) {
	value, err := s.GetSetting(key)
	if err != nil {
		return "", err
	}

	if value == "" {
		return fallback, nil
	}

	return value, nil
}
