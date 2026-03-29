package db

import (
	"database/sql"
	"fmt"
	"time"
)

// RipJob represents a row in the rip_jobs table.
type RipJob struct {
	ID           int64
	DriveIndex   int
	DiscName     string
	TitleIndex   int
	TitleName    string
	ContentType  string
	OutputPath   string
	Status       string
	Progress     int
	ErrorMessage string
	SizeBytes    int64
	Duration     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateJob inserts a new rip job and returns the generated ID.
func (s *Store) CreateJob(job RipJob) (int64, error) {
	const q = `
		INSERT INTO rip_jobs
			(drive_index, disc_name, title_index, title_name, content_type,
			 output_path, status, progress, error_message, size_bytes, duration)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	status := job.Status
	if status == "" {
		status = "pending"
	}

	res, err := s.db.Exec(q,
		job.DriveIndex, job.DiscName, job.TitleIndex, job.TitleName,
		job.ContentType, job.OutputPath, status, job.Progress,
		job.ErrorMessage, job.SizeBytes, job.Duration,
	)
	if err != nil {
		return 0, fmt.Errorf("create job: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}

	return id, nil
}

// GetJob retrieves a rip job by ID.
func (s *Store) GetJob(id int64) (*RipJob, error) {
	const q = `
		SELECT id, drive_index, disc_name, title_index, title_name, content_type,
		       output_path, status, progress, error_message, size_bytes, duration,
		       created_at, updated_at
		FROM rip_jobs WHERE id = ?`

	row := s.db.QueryRow(q, id)
	job, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get job %d: %w", id, err)
	}

	return job, nil
}

// UpdateJobStatus updates the status, progress, and error_message of a rip job.
func (s *Store) UpdateJobStatus(id int64, status string, progress int, errMsg string) error {
	const q = `
		UPDATE rip_jobs
		SET status = ?, progress = ?, error_message = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`

	_, err := s.db.Exec(q, status, progress, errMsg, id)
	if err != nil {
		return fmt.Errorf("update job status %d: %w", id, err)
	}

	return nil
}

// UpdateJobOutput updates the output_path of a rip job.
func (s *Store) UpdateJobOutput(id int64, outputPath string) error {
	const q = `
		UPDATE rip_jobs
		SET output_path = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?`

	_, err := s.db.Exec(q, outputPath, id)
	if err != nil {
		return fmt.Errorf("update job output %d: %w", id, err)
	}

	return nil
}

// ListJobsByStatus returns all rip jobs matching status, ordered by created_at DESC.
func (s *Store) ListJobsByStatus(status string) ([]RipJob, error) {
	const q = `
		SELECT id, drive_index, disc_name, title_index, title_name, content_type,
		       output_path, status, progress, error_message, size_bytes, duration,
		       created_at, updated_at
		FROM rip_jobs
		WHERE status = ?
		ORDER BY created_at DESC`

	return s.queryJobs(q, status)
}

// ListAllJobs returns all rip jobs ordered by created_at DESC with pagination.
func (s *Store) ListAllJobs(limit, offset int) ([]RipJob, error) {
	const q = `
		SELECT id, drive_index, disc_name, title_index, title_name, content_type,
		       output_path, status, progress, error_message, size_bytes, duration,
		       created_at, updated_at
		FROM rip_jobs
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?`

	return s.queryJobs(q, limit, offset)
}

func (s *Store) queryJobs(query string, args ...any) ([]RipJob, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query jobs: %w", err)
	}
	defer rows.Close()

	var jobs []RipJob
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, *job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return jobs, nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanJob(s scanner) (*RipJob, error) {
	var job RipJob
	err := s.Scan(
		&job.ID, &job.DriveIndex, &job.DiscName, &job.TitleIndex, &job.TitleName,
		&job.ContentType, &job.OutputPath, &job.Status, &job.Progress,
		&job.ErrorMessage, &job.SizeBytes, &job.Duration,
		&job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &job, nil
}
