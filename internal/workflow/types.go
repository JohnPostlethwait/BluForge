package workflow

import (
	"fmt"
	"strings"

	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

// TitleSelection represents a user's choice of which title to rip and its metadata.
type TitleSelection struct {
	TitleIndex   int
	TitleName    string
	SourceFile   string
	SizeBytes    int64
	ContentType  string // "movie", "series", "extra", or "" for unmatched
	ContentTitle string
	Year         string
	Season       string
	Episode      string
	EpisodeTitle string
}

// ManualRipParams holds everything needed to initiate a manual rip from the UI.
type ManualRipParams struct {
	DriveIndex      int
	DiscName        string
	DiscKey         string
	Titles          []TitleSelection
	OutputDir       string
	DuplicateAction string
	// Mapping metadata — saved for future lookups.
	MediaItemID string
	ReleaseID   string
	DiscID      string
	MediaTitle  string
	MediaYear   string
	MediaType   string
	// SelectionOpts holds disc-level track selection criteria (audio/subtitle
	// language filtering). Nil means use makemkvcon defaults.
	SelectionOpts *makemkv.SelectionOpts
}

// AutoRipConfig holds config values snapshotted at event time.
type AutoRipConfig struct {
	OutputDir       string
	DuplicateAction string
	SelectionOpts   *makemkv.SelectionOpts // from settings
}

// TitleResult reports the outcome of submitting a single title for ripping.
type TitleResult struct {
	TitleIndex int
	Status     string // "submitted", "skipped", "failed"
	Reason     string // empty on success, explanation on skip/fail
}

// RipResult aggregates the outcomes of all title submissions.
type RipResult struct {
	Titles []TitleResult
}

// HasErrors returns true if any title failed or was skipped.
func (r *RipResult) HasErrors() bool {
	for _, t := range r.Titles {
		if t.Status != "submitted" {
			return true
		}
	}
	return false
}

// ErrorSummary returns a human-readable summary of failures/skips.
func (r *RipResult) ErrorSummary() string {
	var msgs []string
	for _, t := range r.Titles {
		if t.Status == "failed" {
			msgs = append(msgs, fmt.Sprintf("Title %d: %s", t.TitleIndex, t.Reason))
		} else if t.Status == "skipped" {
			msgs = append(msgs, fmt.Sprintf("Title %d: skipped (%s)", t.TitleIndex, t.Reason))
		}
	}
	return strings.Join(msgs, "; ")
}
