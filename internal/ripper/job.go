package ripper

import (
	"context"
	"sync"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
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
// JSON tags use uppercase names to match the existing SSE contract consumed
// by Alpine.js in drive_detail.templ and queue.templ.
type Job struct {
	mu          sync.Mutex `json:"-"`
	ID          int64      `json:"ID"`
	DriveIndex  int        `json:"DriveIndex"`
	TitleIndex  int        `json:"TitleIndex"`
	DiscName    string     `json:"DiscName"`
	TitleName   string     `json:"TitleName"`
	ContentType string     `json:"ContentType,omitempty"`
	OutputDir   string     `json:"-"`
	OutputPath  string     `json:"-"`
	Status      JobStatus  `json:"Status"`
	Progress    int        `json:"Progress"`
	Error       string     `json:"Error,omitempty"`
	StartedAt   time.Time  `json:"StartedAt"`
	FinishedAt  time.Time  `json:"FinishedAt"`
	// OnStart is an optional callback invoked just before the rip begins.
	// Returning a non-nil error aborts the job and transitions it to Failed.
	// Typical use: lazy creation of the per-title temp directory.
	OnStart func(job *Job) error `json:"-"`
	// OnComplete is an optional callback invoked after the job finishes and is
	// removed from the engine's active map. err is nil on success.
	OnComplete func(job *Job, err error) `json:"-"`
	// SelectionOpts holds optional track selection criteria for this job.
	// Not serialized — used only during rip execution.
	SelectionOpts *makemkv.SelectionOpts `json:"-"`
	// cancel stops the rip in progress. Set by the engine when the job starts.
	cancel context.CancelFunc `json:"-"`
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

// Snapshot returns a consistent copy of the job's exported fields.
// Safe to call from any goroutine.
func (j *Job) Snapshot() Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	return Job{
		ID:          j.ID,
		DriveIndex:  j.DriveIndex,
		TitleIndex:  j.TitleIndex,
		DiscName:    j.DiscName,
		TitleName:   j.TitleName,
		ContentType: j.ContentType,
		Status:      j.Status,
		Progress:    j.Progress,
		Error:       j.Error,
		StartedAt:   j.StartedAt,
		FinishedAt:  j.FinishedAt,
	}
}

// Cancel stops a running job by cancelling its context.
func (j *Job) Cancel() {
	j.mu.Lock()
	fn := j.cancel
	j.mu.Unlock()
	if fn != nil {
		fn()
	}
}
