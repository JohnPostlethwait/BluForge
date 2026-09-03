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

// AudioTrack describes a single audio stream on a title.
type AudioTrack struct {
	Language string // e.g. "English"
	Codec    string // e.g. "TrueHD"
	Channels string // e.g. "7.1"
}

// TrackMetadata holds rich metadata about a title captured at scan time.
type TrackMetadata struct {
	SizeBytes         int64
	SizeHuman         string
	Duration          string
	AudioTracks       []AudioTrack
	SubtitleLanguages []string
}

// Job tracks the state of a single title rip operation.
// JSON tags use uppercase names to match the existing SSE contract consumed
// by Alpine.js in drive_detail.templ and queue.templ.
type Job struct {
	mu         sync.Mutex `json:"-"`
	ID         int64      `json:"ID"`
	DriveIndex int        `json:"DriveIndex"`
	TitleIndex int        `json:"TitleIndex"`
	// SourceFile is the playlist or stream this title came from, e.g.
	// "00000.mpls". It is checked against makemkvcon's enumeration at rip time:
	// the index alone is not stable between invocations on a disc whose titles
	// fail to read, and trusting it rips the wrong title under the right name.
	SourceFile  string    `json:"SourceFile,omitempty"`
	DiscName    string    `json:"DiscName"`
	TitleName   string    `json:"TitleName"`
	ContentType string    `json:"ContentType,omitempty"`
	OutputDir   string    `json:"-"`
	OutputPath  string    `json:"-"`
	Status      JobStatus `json:"Status"`
	Progress    int       `json:"Progress"`
	// Phase distinguishes MakeMKV's preliminary analysis from the copy itself.
	// Progress is reported through both, so without it the UI multiplied an
	// analysis percentage by the title's estimated size and presented the
	// result as bytes written -- "4.7 GB / 67.4 GB" before a byte had been
	// copied.
	Phase      string    `json:"Phase,omitempty"`
	Error      string    `json:"Error,omitempty"`
	StartedAt  time.Time `json:"StartedAt"`
	FinishedAt time.Time `json:"FinishedAt"`
	// FailureOutput is what MakeMKV said during a rip that failed, repeats
	// collapsed into counts. Empty on a rip that succeeded.
	//
	// It is not written to rip_jobs, by decision: a job reloaded from the
	// database after a restart has its error and none of this. The page showing
	// it has to treat that absence as ordinary, because it is the state most
	// failed jobs will be in by the time anyone looks.
	FailureOutput []makemkv.ScanWarning `json:"FailureOutput,omitempty"`
	// FailureOutputDropped counts distinct messages the capture turned away at
	// its limit. Non-zero means FailureOutput is incomplete and must not be
	// presented as the whole account.
	FailureOutputDropped int `json:"FailureOutputDropped,omitempty"`
	// OnStart is an optional callback invoked just before the rip begins.
	// Returning a non-nil error aborts the job and transitions it to Failed.
	// Typical use: lazy creation of the per-title temp directory.
	OnStart func(job *Job) error `json:"-"`
	// OnComplete is an optional callback invoked after the rip finishes and the
	// job is removed from the engine's active map. err is the rip's own error,
	// nil when the rip succeeded.
	//
	// It runs while the job is Organizing, and what it returns decides how the
	// job ends: the move to the final destination happens in here, and a rip
	// whose file could not be placed has not succeeded. The engine used to
	// settle the job before calling this, so a failed move was announced as a
	// completed rip and only the database disagreed.
	OnComplete func(job *Job, err error) error `json:"-"`
	// Source is what makemkvcon reads from. Normally the drive named by
	// DriveIndex, but a disc recovered from a spurious AACS directory is ripped
	// from its stripped backup folder instead. Not serialized — the UI groups
	// jobs by drive either way.
	Source makemkv.Source `json:"-"`
	// SelectionOpts holds optional track selection criteria for this job.
	// Not serialized — used only during rip execution.
	SelectionOpts *makemkv.SelectionOpts `json:"-"`
	// TrackMetadata holds scan-time metadata for this title.
	// Included in JSON serialization so SSE broadcasts carry it.
	TrackMetadata TrackMetadata `json:"TrackMetadata,omitempty"`
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
		Source:     makemkv.DiscSource(driveIndex),
	}
}

// RipSource returns the source this job should be ripped from, falling back to
// the job's drive when none was set explicitly. The fallback matters because
// jobs are also built by hand in tests and by older call sites, and a zero
// Source would otherwise silently mean "drive 0".
func (j *Job) RipSource() makemkv.Source {
	if j.Source.Kind == makemkv.SourceFile && j.Source.Path != "" {
		return j.Source
	}
	return makemkv.DiscSource(j.DriveIndex)
}

// Start transitions the job to the Ripping state and records the start time.
func (j *Job) Start() {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = StatusRipping
	j.StartedAt = time.Now()
}

// Rip phases. A job reports no phase until its rip actually starts.
const (
	// PhaseAnalyzing is MakeMKV reading the disc structure. Progress advances
	// during it, but nothing is being written.
	PhaseAnalyzing = "analyzing"
	// PhaseCopying is the copy itself, where progress means what it appears to.
	PhaseCopying = "copying"
)

// SetPhase records which part of the rip is running.
func (j *Job) SetPhase(phase string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Phase = phase
}

// CurrentPhase reports the running part of the rip.
func (j *Job) CurrentPhase() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.Phase
}

// TitleIndex has no setter, deliberately.
//
// There was one. makemkvcon renumbers titles between invocations on a disc
// whose titles fail to read, so a job's index could be corrected mid-rip to
// wherever the guard believed the title had gone. That correction is a guess
// derived from matching a filename against an enumeration still arriving, and
// on a multi-angle disc the filename does not identify a title at all: Kiki's
// Delivery Service announces both angles as 00200.mpls. The guard misread an
// angle number as a title number, the job for title 3 was re-pointed at title
// 1, and a different cut of the film was copied and filed under the requested
// title's name — reported as a success.
//
// The index a job was created for is the only index it may ever rip. Leaving
// no way to write the field is what keeps that true, rather than a comment
// asking the next caller not to.

// SetStatus sets the job's lifecycle state without touching its timestamps.
// Used for the non-terminal transitions; Complete, Fail and Skip settle a job.
func (j *Job) SetStatus(status JobStatus) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Status = status
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
//
// Every field the UI reads has to be listed here. An omission is silent: the
// field marshals as its zero value and the page quietly shows the wrong thing.
func (j *Job) Snapshot() Job {
	j.mu.Lock()
	defer j.mu.Unlock()
	return Job{
		ID:            j.ID,
		DriveIndex:    j.DriveIndex,
		TitleIndex:    j.TitleIndex,
		DiscName:      j.DiscName,
		TitleName:     j.TitleName,
		ContentType:   j.ContentType,
		SourceFile:    j.SourceFile,
		Status:        j.Status,
		Progress:      j.Progress,
		Phase:         j.Phase,
		Error:         j.Error,
		StartedAt:     j.StartedAt,
		FinishedAt:    j.FinishedAt,
		TrackMetadata: j.TrackMetadata,

		FailureOutput:        j.FailureOutput,
		FailureOutputDropped: j.FailureOutputDropped,
	}
}

// SetFailureOutput records what MakeMKV said during a rip that failed.
//
// Through a setter for the same reason as the other fields: this runs on the
// rip goroutine while the activity and dashboard pages read the job through
// Snapshot.
func (j *Job) SetFailureOutput(messages []makemkv.ScanWarning, dropped int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.FailureOutput = messages
	j.FailureOutputDropped = dropped
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
