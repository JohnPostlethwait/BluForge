package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/organizer"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
)

// DiscScanner abstracts disc scanning for testability.
type DiscScanner interface {
	ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error)
}

// OrchestratorDeps holds the dependencies required to construct an Orchestrator.
type OrchestratorDeps struct {
	Store       *db.Store
	Engine      *ripper.Engine
	Organizer   *organizer.Organizer
	OnBroadcast func(event, data string)
	Scanner     DiscScanner
	DiscDB      *discdb.Client
	Cache       *discdb.Cache
}

// Orchestrator coordinates the end-to-end rip pipeline: disk space check,
// destination path construction, duplicate detection, DB job creation,
// engine submission, and completion handling.
type Orchestrator struct {
	store       *db.Store
	engine      *ripper.Engine
	organizer   *organizer.Organizer
	onBroadcast func(event, data string)
	scanner     DiscScanner
	discDB      *discdb.Client
	cache       *discdb.Cache

	scanMu    sync.RWMutex
	scanCache map[string]*makemkv.DiscScan // keyed by "driveIndex:discName"
}

// NewOrchestrator creates a new Orchestrator from the provided dependencies.
func NewOrchestrator(deps OrchestratorDeps) *Orchestrator {
	return &Orchestrator{
		store:       deps.Store,
		engine:      deps.Engine,
		organizer:   deps.Organizer,
		onBroadcast: deps.OnBroadcast,
		scanner:     deps.Scanner,
		discDB:      deps.DiscDB,
		cache:       deps.Cache,
		scanCache:   make(map[string]*makemkv.DiscScan),
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

// ScanDisc delegates disc scanning to the configured scanner. Results are cached
// per drive+disc combination so that repeated visits to the drive detail page
// don't re-read the physical disc each time.
func (o *Orchestrator) ScanDisc(ctx context.Context, driveIndex int) (*makemkv.DiscScan, error) {
	if o.scanner == nil {
		return nil, fmt.Errorf("no scanner configured")
	}

	scan, err := o.scanner.ScanDisc(ctx, driveIndex)
	if err != nil {
		return nil, err
	}

	key := fmt.Sprintf("%d:%s", driveIndex, scan.DiscName)
	o.scanMu.Lock()
	o.scanCache[key] = scan
	o.scanMu.Unlock()

	return scan, nil
}

// CachedScan returns a previously cached scan for the given drive and disc name,
// or nil if no cached result exists.
func (o *Orchestrator) CachedScan(driveIndex int, discName string) *makemkv.DiscScan {
	key := fmt.Sprintf("%d:%s", driveIndex, discName)
	o.scanMu.RLock()
	defer o.scanMu.RUnlock()
	return o.scanCache[key]
}

// InvalidateScan removes any cached scan for the given drive index.
func (o *Orchestrator) InvalidateScan(driveIndex int) {
	o.scanMu.Lock()
	defer o.scanMu.Unlock()
	for key := range o.scanCache {
		prefix := fmt.Sprintf("%d:", driveIndex)
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(o.scanCache, key)
		}
	}
}

// AutoRip scans a disc, attempts to auto-match it against TheDiscDB, and
// submits all titles for ripping. If a cached disc mapping exists, it is used
// directly; otherwise the disc name is searched via the DiscDB client.
func (o *Orchestrator) AutoRip(ctx context.Context, driveIndex int, cfg AutoRipConfig) error {
	scan, err := o.ScanDisc(ctx, driveIndex)
	if err != nil {
		return fmt.Errorf("auto-rip scan: %w", err)
	}

	discKey := discdb.BuildDiscKey(scan)

	// Check for an existing disc mapping.
	mapping, err := o.store.GetMapping(discKey)
	if err != nil {
		return fmt.Errorf("auto-rip get mapping: %w", err)
	}

	var titles []TitleSelection
	var mediaItemID, releaseID, mediaTitle, mediaYear, mediaType string

	if mapping != nil {
		slog.Info("auto-rip: using cached disc mapping",
			"disc_key", discKey, "media_title", mapping.MediaTitle)
		titles = o.titlesFromMapping(scan, mapping)
		mediaItemID = mapping.MediaItemID
		releaseID = mapping.ReleaseID
		mediaTitle = mapping.MediaTitle
		mediaYear = mapping.MediaYear
		mediaType = mapping.MediaType
	} else {
		titles, mediaItemID, releaseID, mediaTitle, mediaYear, mediaType = o.autoMatch(ctx, scan)
	}

	params := ManualRipParams{
		DriveIndex:      driveIndex,
		DiscName:        scan.DiscName,
		DiscKey:         discKey,
		Titles:          titles,
		OutputDir:       cfg.OutputDir,
		MovieTemplate:   cfg.MovieTemplate,
		SeriesTemplate:  cfg.SeriesTemplate,
		DuplicateAction: cfg.DuplicateAction,
		MediaItemID:     mediaItemID,
		ReleaseID:       releaseID,
		MediaTitle:      mediaTitle,
		MediaYear:       mediaYear,
		MediaType:       mediaType,
	}

	result := o.ManualRip(params)
	if result.HasErrors() {
		slog.Warn("auto-rip completed with errors", "summary", result.ErrorSummary())
	}

	return nil
}

// Rescan scans the disc and deletes any existing disc mapping so that
// the next AutoRip performs a fresh lookup.
func (o *Orchestrator) Rescan(ctx context.Context, driveIndex int) error {
	scan, err := o.ScanDisc(ctx, driveIndex)
	if err != nil {
		return fmt.Errorf("rescan: %w", err)
	}

	discKey := discdb.BuildDiscKey(scan)
	if err := o.store.DeleteMapping(discKey); err != nil {
		return fmt.Errorf("rescan delete mapping: %w", err)
	}

	slog.Info("rescan: deleted disc mapping", "disc_key", discKey, "disc_name", scan.DiscName)
	return nil
}

// titlesFromMapping builds TitleSelections using a saved disc mapping for all
// titles in the scan.
func (o *Orchestrator) titlesFromMapping(scan *makemkv.DiscScan, mapping *db.DiscMapping) []TitleSelection {
	titles := make([]TitleSelection, 0, len(scan.Titles))
	for _, t := range scan.Titles {
		var sizeBytes int64
		if s := t.SizeBytes(); s != "" {
			fmt.Sscanf(s, "%d", &sizeBytes)
		}
		titles = append(titles, TitleSelection{
			TitleIndex:   t.Index,
			TitleName:    t.Name(),
			SourceFile:   t.SourceFile(),
			SizeBytes:    sizeBytes,
			ContentType:  mapping.MediaType,
			ContentTitle: mapping.MediaTitle,
			Year:         mapping.MediaYear,
		})
	}
	return titles
}

// autoMatch searches TheDiscDB for the disc name, scores matches, and returns
// title selections along with metadata. Falls back to unmatched titles if no
// confident match is found.
func (o *Orchestrator) autoMatch(ctx context.Context, scan *makemkv.DiscScan) (
	titles []TitleSelection,
	mediaItemID, releaseID, mediaTitle, mediaYear, mediaType string,
) {
	if o.discDB != nil && scan.DiscName != "" {
		items, err := o.discDB.SearchByTitle(ctx, scan.DiscName)
		if err != nil {
			slog.Warn("auto-rip: discdb search failed", "error", err)
		} else if len(items) > 0 {
			best, score := discdb.BestRelease(scan, items)
			if best != nil && score >= 10 {
				slog.Info("auto-rip: matched via discdb",
					"title", best.MediaItem.Title, "score", score)
				titles = o.titlesFromSearchResult(scan, best)
				mediaItemID = strconv.Itoa(best.MediaItem.ID)
				releaseID = strconv.Itoa(best.Release.ID)
				mediaTitle = best.MediaItem.Title
				mediaYear = strconv.Itoa(best.MediaItem.Year)
				mediaType = best.MediaItem.Type
				return
			}
		}
	}

	slog.Info("auto-rip: no confident match, using unmatched titles",
		"disc_name", scan.DiscName)
	titles = o.unmatchedTitles(scan)
	return
}

// titlesFromSearchResult builds TitleSelections from a TheDiscDB match using
// MatchTitles to correlate scan titles with disc metadata.
func (o *Orchestrator) titlesFromSearchResult(scan *makemkv.DiscScan, sr *discdb.SearchResult) []TitleSelection {
	matches := discdb.MatchTitles(scan, sr.Disc)
	titles := make([]TitleSelection, 0, len(scan.Titles))

	for _, cm := range matches {
		// Find the scan title for size info.
		var sizeBytes int64
		var titleName string
		for _, t := range scan.Titles {
			if t.Index == cm.TitleIndex {
				if s := t.SizeBytes(); s != "" {
					fmt.Sscanf(s, "%d", &sizeBytes)
				}
				titleName = t.Name()
				break
			}
		}

		sel := TitleSelection{
			TitleIndex: cm.TitleIndex,
			TitleName:  titleName,
			SourceFile: cm.SourceFile,
			SizeBytes:  sizeBytes,
		}

		if cm.Matched {
			sel.ContentType = cm.ContentType
			sel.ContentTitle = cm.ContentTitle
			sel.Season = cm.Season
			sel.Episode = cm.Episode
		}

		titles = append(titles, sel)
	}

	return titles
}

// unmatchedTitles builds TitleSelections with no content metadata — the
// organizer will place them in an unmatched directory.
func (o *Orchestrator) unmatchedTitles(scan *makemkv.DiscScan) []TitleSelection {
	titles := make([]TitleSelection, 0, len(scan.Titles))
	for _, t := range scan.Titles {
		var sizeBytes int64
		if s := t.SizeBytes(); s != "" {
			fmt.Sscanf(s, "%d", &sizeBytes)
		}
		titles = append(titles, TitleSelection{
			TitleIndex: t.Index,
			TitleName:  t.Name(),
			SourceFile: t.SourceFile(),
			SizeBytes:  sizeBytes,
		})
	}
	return titles
}

// broadcastJobUpdate sends a job status update over SSE.
func (o *Orchestrator) broadcastJobUpdate(jobID int64, status string) {
	if o.onBroadcast == nil {
		return
	}
	data, err := json.Marshal(map[string]any{
		"job_id": jobID,
		"status": status,
	})
	if err != nil {
		slog.Error("failed to marshal SSE data", "error", err)
		return
	}
	o.onBroadcast("job_update", string(data))
}
