package workflow

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/internal/web"
)

// OrchestratorDeps holds the dependencies required to construct an Orchestrator.
type OrchestratorDeps struct {
	Store     *db.Store
	Engine    *ripper.Engine
	Organizer *organizer.Organizer
	SSEHub    *web.SSEHub
}

// Orchestrator coordinates the end-to-end rip pipeline: disk space check,
// destination path construction, duplicate detection, DB job creation,
// engine submission, and completion handling.
type Orchestrator struct {
	store     *db.Store
	engine    *ripper.Engine
	organizer *organizer.Organizer
	sseHub    *web.SSEHub
}

// NewOrchestrator creates a new Orchestrator from the provided dependencies.
func NewOrchestrator(deps OrchestratorDeps) *Orchestrator {
	return &Orchestrator{
		store:     deps.Store,
		engine:    deps.Engine,
		organizer: deps.Organizer,
		sseHub:    deps.SSEHub,
	}
}

// ManualRip processes each title in params, building destination paths,
// checking for duplicates, creating DB records, and submitting rip jobs
// to the engine. It returns a RipResult summarising the outcome of each title.
func (o *Orchestrator) ManualRip(params ManualRipParams) RipResult {
	var result RipResult

	for _, sel := range params.Titles {
		tr := o.processTitle(params, sel)
		result.Titles = append(result.Titles, tr)
	}

	// Save disc mapping if we have the necessary identifiers.
	if params.DiscKey != "" && params.MediaItemID != "" {
		if err := o.store.SaveMapping(db.DiscMapping{
			DiscKey:     params.DiscKey,
			DiscName:    params.DiscName,
			MediaItemID: params.MediaItemID,
			ReleaseID:   params.ReleaseID,
			MediaTitle:  params.MediaTitle,
			MediaYear:   params.MediaYear,
			MediaType:   params.MediaType,
		}); err != nil {
			slog.Error("failed to save disc mapping", "disc_key", params.DiscKey, "error", err)
		}
	}

	return result
}

// processTitle handles a single title: build path, duplicate check, disk space,
// DB creation, engine submission.
func (o *Orchestrator) processTitle(params ManualRipParams, sel TitleSelection) TitleResult {
	// 1. Build destination path via organizer based on content type.
	destPath, err := o.buildDestPath(params, sel)
	if err != nil {
		return TitleResult{
			TitleIndex: sel.TitleIndex,
			Status:     "failed",
			Reason:     fmt.Sprintf("build destination path: %v", err),
		}
	}

	fullDest := filepath.Join(params.OutputDir, destPath)

	// 2. Check for duplicates.
	if organizer.FileExists(fullDest) && params.DuplicateAction == "skip" {
		return TitleResult{
			TitleIndex: sel.TitleIndex,
			Status:     "skipped",
			Reason:     fmt.Sprintf("duplicate exists: %s", destPath),
		}
	}

	// 3. Check disk space.
	if err := ripper.CheckDiskSpace(params.OutputDir, sel.SizeBytes); err != nil {
		return TitleResult{
			TitleIndex: sel.TitleIndex,
			Status:     "failed",
			Reason:     fmt.Sprintf("disk space: %v", err),
		}
	}

	// 4. Create DB job.
	jobID, err := o.store.CreateJob(db.RipJob{
		DriveIndex:  params.DriveIndex,
		DiscName:    params.DiscName,
		TitleIndex:  sel.TitleIndex,
		TitleName:   sel.TitleName,
		ContentType: sel.ContentType,
		OutputPath:  fullDest,
		Status:      "ripping",
		SizeBytes:   sel.SizeBytes,
	})
	if err != nil {
		return TitleResult{
			TitleIndex: sel.TitleIndex,
			Status:     "failed",
			Reason:     fmt.Sprintf("create job: %v", err),
		}
	}

	// 5. Create ripper job with OnComplete hook.
	ripJob := ripper.NewJob(params.DriveIndex, sel.TitleIndex, params.DiscName, params.OutputDir)
	ripJob.ID = jobID
	ripJob.TitleName = sel.TitleName
	ripJob.OnComplete = func(job *ripper.Job, ripErr error) {
		if ripErr != nil {
			if dbErr := o.store.UpdateJobStatus(job.ID, "failed", job.Progress, ripErr.Error()); dbErr != nil {
				slog.Error("failed to update job status", "job_id", job.ID, "error", dbErr)
			}
			o.broadcastJobUpdate(job.ID, "failed")
			return
		}

		if dbErr := o.store.UpdateJobOutput(job.ID, fullDest); dbErr != nil {
			slog.Error("failed to update job output", "job_id", job.ID, "error", dbErr)
		}
		if dbErr := o.store.UpdateJobStatus(job.ID, "completed", 100, ""); dbErr != nil {
			slog.Error("failed to update job status", "job_id", job.ID, "error", dbErr)
		}
		o.broadcastJobUpdate(job.ID, "completed")
	}

	// 6. Submit to engine.
	if err := o.engine.Submit(ripJob); err != nil {
		if dbErr := o.store.UpdateJobStatus(jobID, "failed", 0, err.Error()); dbErr != nil {
			slog.Error("failed to update job status on submit failure", "job_id", jobID, "error", dbErr)
		}
		return TitleResult{
			TitleIndex: sel.TitleIndex,
			Status:     "failed",
			Reason:     fmt.Sprintf("submit to engine: %v", err),
		}
	}

	return TitleResult{
		TitleIndex: sel.TitleIndex,
		Status:     "submitted",
	}
}

// buildDestPath selects the appropriate organizer method based on content type.
func (o *Orchestrator) buildDestPath(params ManualRipParams, sel TitleSelection) (string, error) {
	switch sel.ContentType {
	case "movie":
		return o.organizer.BuildMoviePath(organizer.MovieMeta{
			Title: sel.ContentTitle,
			Year:  sel.Year,
		})
	case "series":
		return o.organizer.BuildSeriesPath(organizer.SeriesMeta{
			Show:         sel.ContentTitle,
			Season:       sel.Season,
			Episode:      sel.Episode,
			EpisodeTitle: sel.EpisodeTitle,
		})
	case "extra":
		return o.organizer.BuildExtrasPath(organizer.ExtraMeta{
			Title:       sel.ContentTitle,
			Year:        sel.Year,
			Show:        sel.ContentTitle,
			Season:      sel.Season,
			ExtraTitle:  sel.TitleName,
			ContentType: params.MediaType,
		}), nil
	default:
		return o.organizer.BuildUnmatchedPath(params.DiscName, sel.SourceFile), nil
	}
}

// broadcastJobUpdate sends a job status update over SSE.
func (o *Orchestrator) broadcastJobUpdate(jobID int64, status string) {
	data, err := json.Marshal(map[string]any{
		"job_id": jobID,
		"status": status,
	})
	if err != nil {
		slog.Error("failed to marshal SSE data", "error", err)
		return
	}
	o.sseHub.Broadcast(web.SSEEvent{
		Event: "job_update",
		Data:  string(data),
	})
}
