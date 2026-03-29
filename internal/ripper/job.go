package ripper

import (
	"sync"
	"time"
)

// JobStatus represents the lifecycle state of a rip job.
type JobStatus string

const (
	StatusPending    JobStatus = "pending"
	StatusRipping    JobStatus = "ripping"
	StatusOrganizing JobStatus = "organizing"
	StatusCompleted  JobStatus = "completed"
	StatusFailed     JobStatus = "failed"
	StatusSkipped    JobStatus = "skipped"
)

// Job tracks the state of a single title rip operation.
type Job struct {
	mu         sync.Mutex
	ID         int64
	DriveIndex int
	TitleIndex int
	DiscName   string
	TitleName  string
	OutputDir  string
	OutputPath string
	Status     JobStatus
	Progress   int
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
	// OnComplete is an optional callback invoked after the job finishes and is
	// removed from the engine's active map. err is nil on success.
	OnComplete func(job *Job, err error)
}

// NewJob creates a new Job in the Pending state.
func NewJob(driveIndex, titleIndex int, discName, outputDir string) *Job {
	return &Job{
		DriveIndex: driveIndex,
		TitleIndex: titleIndex,
		DiscName:   discName,
		OutputDir:  outputDir,
		Status:     StatusPending,
	}
}

// Start transitions the job to the Ripping state and records the start time.
func (j *Job) Start() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = StatusRipping
	j.StartedAt = time.Now()
}

// UpdateProgress sets the progress percentage (0-100).
func (j *Job) UpdateProgress(pct int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Progress = pct
}

// Complete transitions the job to Completed, sets OutputPath, and records
// the finish time.
func (j *Job) Complete(outputPath string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.OutputPath = outputPath
	j.Status = StatusCompleted
	j.Progress = 100
	j.FinishedAt = time.Now()
}

// Fail transitions the job to Failed with the given error message.
func (j *Job) Fail(errMsg string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = StatusFailed
	j.Error = errMsg
	j.FinishedAt = time.Now()
}

// Skip transitions the job to Skipped.
func (j *Job) Skip() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = StatusSkipped
	j.FinishedAt = time.Now()
}
