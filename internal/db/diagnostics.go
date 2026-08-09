package db

import (
	"database/sql"
	"fmt"
	"time"
)

// DiscDiagnostic records how one disc was read and which rip path it took.
//
// The point is recurrence: a spurious AACS directory shows up on scattered
// discs with no predictable pattern, so the evidence has to be persisted at the
// moment it is gathered or the investigation gets repeated from scratch.
type DiscDiagnostic struct {
	ID               int64
	DiscLabel        string
	DiscKey          string
	DriveIndex       int
	MKBVersion       string
	AACSDirPresent   bool
	ScrambleVerdict  string // unencrypted | scrambled | unknown | n/a
	PacketsChecked   int
	ScrambledPackets int
	RipPath          string // direct | backup_strip | blocked
	Outcome          string // ok | failed
	Detail           string
	DumpPath         string
	BackupBytes      int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// SaveDiscDiagnostic inserts a diagnostic row and returns its ID.
func (s *Store) SaveDiscDiagnostic(d DiscDiagnostic) (int64, error) {
	const q = `
		INSERT INTO disc_diagnostics
			(disc_label, disc_key, drive_index, mkb_version, aacs_dir_present,
			 scramble_verdict, packets_checked, scrambled_packets, rip_path,
			 outcome, detail, dump_path, backup_bytes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	res, err := s.db.Exec(q,
		d.DiscLabel, d.DiscKey, d.DriveIndex, d.MKBVersion, d.AACSDirPresent,
		d.ScrambleVerdict, d.PacketsChecked, d.ScrambledPackets, d.RipPath,
		d.Outcome, d.Detail, d.DumpPath, d.BackupBytes)
	if err != nil {
		return 0, fmt.Errorf("save disc diagnostic: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("save disc diagnostic last insert id: %w", err)
	}
	return id, nil
}

// UpdateDiscDiagnosticOutcome finishes a row opened before recovery started.
// backupBytes is recorded even on failure, since a partial backup is what the
// user will find left on disk.
func (s *Store) UpdateDiscDiagnosticOutcome(id int64, outcome, detail string, backupBytes int64) error {
	const q = `
		UPDATE disc_diagnostics
		SET outcome = ?, detail = ?, backup_bytes = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`

	if _, err := s.db.Exec(q, outcome, detail, backupBytes, id); err != nil {
		return fmt.Errorf("update disc diagnostic outcome: %w", err)
	}
	return nil
}

// SetDiscDiagnosticKey records the disc key once a scan has produced titles.
// Before that point the key cannot be computed meaningfully.
func (s *Store) SetDiscDiagnosticKey(id int64, discKey string) error {
	const q = `UPDATE disc_diagnostics SET disc_key = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	if _, err := s.db.Exec(q, discKey, id); err != nil {
		return fmt.Errorf("set disc diagnostic key: %w", err)
	}
	return nil
}

// GetDiscDiagnostic returns one row by ID, or nil when it does not exist.
func (s *Store) GetDiscDiagnostic(id int64) (*DiscDiagnostic, error) {
	const q = `
		SELECT id, disc_label, disc_key, drive_index, mkb_version, aacs_dir_present,
		       scramble_verdict, packets_checked, scrambled_packets, rip_path,
		       outcome, detail, dump_path, backup_bytes, created_at, updated_at
		FROM disc_diagnostics WHERE id = ?`

	d, err := scanDiscDiagnostic(s.db.QueryRow(q, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get disc diagnostic: %w", err)
	}
	return d, nil
}

// ListDiscDiagnosticsByLabel returns a disc's history, newest first.
func (s *Store) ListDiscDiagnosticsByLabel(label string, limit int) ([]DiscDiagnostic, error) {
	const q = `
		SELECT id, disc_label, disc_key, drive_index, mkb_version, aacs_dir_present,
		       scramble_verdict, packets_checked, scrambled_packets, rip_path,
		       outcome, detail, dump_path, backup_bytes, created_at, updated_at
		FROM disc_diagnostics
		WHERE disc_label = ?
		ORDER BY id DESC
		LIMIT ?`

	rows, err := s.db.Query(q, label, limit)
	if err != nil {
		return nil, fmt.Errorf("list disc diagnostics: %w", err)
	}
	defer rows.Close()

	var out []DiscDiagnostic
	for rows.Next() {
		d, err := scanDiscDiagnostic(rows)
		if err != nil {
			return nil, fmt.Errorf("list disc diagnostics scan: %w", err)
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list disc diagnostics rows: %w", err)
	}
	return out, nil
}

// rowScanner covers both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanDiscDiagnostic(r rowScanner) (*DiscDiagnostic, error) {
	var d DiscDiagnostic
	err := r.Scan(
		&d.ID, &d.DiscLabel, &d.DiscKey, &d.DriveIndex, &d.MKBVersion, &d.AACSDirPresent,
		&d.ScrambleVerdict, &d.PacketsChecked, &d.ScrambledPackets, &d.RipPath,
		&d.Outcome, &d.Detail, &d.DumpPath, &d.BackupBytes, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &d, nil
}
