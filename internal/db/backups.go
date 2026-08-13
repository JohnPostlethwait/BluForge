package db

import (
	"database/sql"
	"fmt"
	"time"
)

// DiscBackup is a scratch copy of a disc taken during recovery.
//
// It is persisted because the copy is expensive — up to ~100GB and tens of
// minutes — and must outlive a restart. Without the record, the startup sweep
// cannot tell a live backup from crash debris, and the next scan would go back
// to a drive MakeMKV is unable to read.
type DiscBackup struct {
	ID         int64
	DriveIndex int
	DiscLabel  string
	BackupDir  string
	SourceArg  string
	// Partial marks a copy that is not finished and not yet rippable — a
	// salvage still running. It is protected from the startup sweep so a
	// restart cannot delete hours of work, but never restored as a disc ready
	// to rip.
	Partial   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SaveDiscBackup records a backup, replacing any earlier record of the same
// directory.
func (s *Store) SaveDiscBackup(b DiscBackup) (int64, error) {
	const q = `
		INSERT INTO disc_backups (drive_index, disc_label, backup_dir, source_arg, partial)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(backup_dir) DO UPDATE SET
			drive_index = excluded.drive_index,
			disc_label  = excluded.disc_label,
			source_arg  = excluded.source_arg,
			partial     = excluded.partial,
			updated_at  = CURRENT_TIMESTAMP`

	res, err := s.db.Exec(q, b.DriveIndex, b.DiscLabel, b.BackupDir, b.SourceArg, b.Partial)
	if err != nil {
		return 0, fmt.Errorf("save disc backup: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("save disc backup last insert id: %w", err)
	}
	return id, nil
}

// ListDiscBackups returns every recorded backup, oldest first.
func (s *Store) ListDiscBackups() ([]DiscBackup, error) {
	const q = `
		SELECT id, drive_index, disc_label, backup_dir, source_arg, partial, created_at, updated_at
		FROM disc_backups ORDER BY id`

	rows, err := s.db.Query(q)
	if err != nil {
		return nil, fmt.Errorf("list disc backups: %w", err)
	}
	defer rows.Close()

	var out []DiscBackup
	for rows.Next() {
		var b DiscBackup
		if err := rows.Scan(&b.ID, &b.DriveIndex, &b.DiscLabel, &b.BackupDir,
			&b.SourceArg, &b.Partial, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("list disc backups scan: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list disc backups rows: %w", err)
	}
	return out, nil
}

// GetDiscBackupForDrive returns the backup recorded for a drive, or nil.
func (s *Store) GetDiscBackupForDrive(driveIndex int) (*DiscBackup, error) {
	const q = `
		SELECT id, drive_index, disc_label, backup_dir, source_arg, created_at, updated_at
		FROM disc_backups WHERE drive_index = ? ORDER BY id DESC LIMIT 1`

	var b DiscBackup
	err := s.db.QueryRow(q, driveIndex).Scan(&b.ID, &b.DriveIndex, &b.DiscLabel,
		&b.BackupDir, &b.SourceArg, &b.CreatedAt, &b.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get disc backup: %w", err)
	}
	return &b, nil
}

// DeleteDiscBackup removes the record for a backup directory.
func (s *Store) DeleteDiscBackup(backupDir string) error {
	if _, err := s.db.Exec(`DELETE FROM disc_backups WHERE backup_dir = ?`, backupDir); err != nil {
		return fmt.Errorf("delete disc backup: %w", err)
	}
	return nil
}
