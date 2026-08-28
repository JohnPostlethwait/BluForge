package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/templates"
)

const activityHistoryPageSize = 50

// activityJobJSON is the Alpine store shape for any job in the activity view,
// covering active, pending, completed, and history states. Fields not relevant
// to a given state are omitted from JSON output via omitempty.
type activityJobJSON struct {
	ID          int64  `json:"id"`
	DiscName    string `json:"discName"`
	TitleName   string `json:"titleName"`
	ContentType string `json:"contentType"`
	Status      string `json:"status"`
	Progress    int    `json:"progress"`
	Error       string `json:"error,omitempty"`
	DriveIndex  int    `json:"driveIndex"`
	FinishedAt  string `json:"finishedAt,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
	// SalvageNote explains damage a salvaged rip knowingly contains. Empty for
	// an ordinary rip.
	SalvageNote string `json:"salvageNote,omitempty"`
	// SalvageResumable marks a disc with a half-finished salvage on disk, so the
	// offer reads "resume" rather than inviting an hour of re-reading.
	SalvageResumable bool `json:"salvageResumable,omitempty"`
	// Salvageable marks a failure the disc might still be recovered from, which
	// is what puts the offer on the card. Only ever an offer: a salvage
	// produces damaged video and is the user's decision to make.
	Salvageable bool `json:"salvageable,omitempty"`
	// Phase is "analyzing" or "copying" for a running rip. Progress advances
	// through both, but only means bytes written during the copy.
	Phase             string              `json:"phase,omitempty"`
	SizeBytes         int64               `json:"sizeBytes,omitempty"`
	SizeHuman         string              `json:"sizeHuman,omitempty"`
	Duration          string              `json:"duration,omitempty"`
	AudioTracks       []ripper.AudioTrack `json:"audioTracks,omitempty"`
	SubtitleLanguages []string            `json:"subtitleLanguages,omitempty"`
	OutputPath        string              `json:"outputPath,omitempty"`
	CreatedAt         string              `json:"createdAt,omitempty"`
}

// activityStoreJSON is the Alpine.store('activity') shape.
type activityStoreJSON struct {
	// Salvage lets a page that just loaded, or reconnected, draw the salvage
	// panel without having seen any events. A salvage runs for hours and can be
	// quiet for minutes at a time.
	Salvage   salvageStateJSON  `json:"salvage"`
	Active    []activityJobJSON `json:"active"`
	Pending   []activityJobJSON `json:"pending"`
	Completed []activityJobJSON `json:"completed"`
	History   []activityJobJSON `json:"history"`
	Page      int               `json:"page"`
	HasMore   bool              `json:"hasMore"`
	// DiscsWithBackup names the discs whose repaired copy is still on disk.
	// History lists every rip that ever came off a copy, but most of those
	// copies are long gone — this is what separates the ones worth offering to
	// delete from the ones that would be a button doing nothing.
	DiscsWithBackup []string `json:"discsWithBackup"`
}

// salvageStateJSON mirrors workflow.SalvageState for the page.
type salvageStateJSON struct {
	Active     bool   `json:"active"`
	Paused     bool   `json:"paused"`
	DriveIndex int    `json:"driveIndex"`
	DiscLabel  string `json:"discLabel"`
	Resumable  bool   `json:"resumable"`
}

// parseTrackMetadata deserializes a raw JSON track_metadata string from the DB.
// Returns a zero-value TrackMetadata on empty input or parse error.
func parseTrackMetadata(raw string) ripper.TrackMetadata {
	if raw == "" {
		return ripper.TrackMetadata{}
	}
	var m ripper.TrackMetadata
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		slog.Warn("failed to parse track_metadata", "error", err)
	}
	return m
}

func (s *Server) handleActivity(c echo.Context) error {
	store := activityStoreJSON{
		Active:    make([]activityJobJSON, 0),
		Pending:   make([]activityJobJSON, 0),
		Completed: make([]activityJobJSON, 0),
		History:   make([]activityJobJSON, 0),
		Page:      1,
	}

	// Active jobs from the rip engine.
	if s.ripEngine != nil {
		for _, j := range s.ripEngine.ActiveJobs() {
			var startedAt string
			if !j.StartedAt.IsZero() {
				startedAt = j.StartedAt.UTC().Format(time.RFC3339)
			}
			store.Active = append(store.Active, activityJobJSON{
				ID:          j.ID,
				DiscName:    j.DiscName,
				TitleName:   j.TitleName,
				ContentType: normalizeContentType(j.ContentType),
				Status:      string(j.Status),
				Progress:    j.Progress,
				Error:       j.Error,
				DriveIndex:  j.DriveIndex,
				StartedAt:   startedAt,
				// A page loaded mid-rip has seen no events, so the phase has to
				// come from the engine or the byte counter starts out lying
				// again until the next update arrives.
				Phase: j.CurrentPhase(),
				// A rip from a salvaged copy carries damage, and the card you
				// watch for twenty minutes said nothing about it.
				SalvageNote:       s.salvageNoteForJob(j.ID),
				SizeBytes:         j.TrackMetadata.SizeBytes,
				SizeHuman:         j.TrackMetadata.SizeHuman,
				Duration:          j.TrackMetadata.Duration,
				AudioTracks:       j.TrackMetadata.AudioTracks,
				SubtitleLanguages: j.TrackMetadata.SubtitleLanguages,
			})
		}

		// Queued (pending) jobs.
		for _, j := range s.ripEngine.QueuedJobs() {
			store.Pending = append(store.Pending, activityJobJSON{
				ID:                j.ID,
				DiscName:          j.DiscName,
				TitleName:         j.TitleName,
				ContentType:       normalizeContentType(j.ContentType),
				Status:            string(j.Status),
				DriveIndex:        j.DriveIndex,
				SizeBytes:         j.TrackMetadata.SizeBytes,
				SizeHuman:         j.TrackMetadata.SizeHuman,
				Duration:          j.TrackMetadata.Duration,
				AudioTracks:       j.TrackMetadata.AudioTracks,
				SubtitleLanguages: j.TrackMetadata.SubtitleLanguages,
			})
		}
	}

	if s.orchestrator != nil {
		cur := s.orchestrator.CurrentSalvage()
		store.Salvage = salvageStateJSON{
			Active:     cur.Active,
			Paused:     cur.Paused,
			DriveIndex: cur.DriveIndex,
			DiscLabel:  cur.DiscLabel,
			Resumable:  cur.Resumable,
		}
	}

	// Recent completed/failed jobs.
	completedJobs, err := s.store.ListJobsByStatus("completed")
	if err != nil {
		slog.Error("failed to list completed jobs", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load completed jobs.")
	}
	failedJobs, err := s.store.ListJobsByStatus("failed")
	if err != nil {
		slog.Error("failed to list failed jobs", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load failed jobs.")
	}

	for _, j := range append(completedJobs, failedJobs...) {
		meta := parseTrackMetadata(j.TrackMetadata)
		store.Completed = append(store.Completed, activityJobJSON{
			ID:                j.ID,
			DiscName:          j.DiscName,
			TitleName:         j.TitleName,
			ContentType:       normalizeContentType(j.ContentType),
			Status:            j.Status,
			Progress:          j.Progress,
			Error:             j.ErrorMessage,
			DriveIndex:        j.DriveIndex,
			FinishedAt:        j.UpdatedAt.Format("Jan 2 15:04"),
			Salvageable:       salvageable(j.Status, j.ErrorMessage),
			SalvageResumable:  s.salvageResumable(j.Status, j.ErrorMessage, j.DiscName),
			SalvageNote:       j.SalvageNote,
			SizeHuman:         deliveredSize(j.OutputSizeBytes, meta.SizeHuman),
			Duration:          meta.Duration,
			AudioTracks:       meta.AudioTracks,
			SubtitleLanguages: meta.SubtitleLanguages,
		})
	}

	// Paginated full history.
	page := 1
	if p := c.QueryParam("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}
	if page > 1000 {
		page = 1000
	}
	store.Page = page

	// Collect IDs of jobs currently tracked by the engine so they can be
	// excluded from the history list. Without this exclusion, active/pending
	// jobs appear in both their respective sections AND in history, producing
	// duplicate Alpine x-for keys which causes DOM reconciliation errors.
	engineIDs := make(map[int64]bool)
	if s.ripEngine != nil {
		for _, j := range s.ripEngine.ActiveJobs() {
			engineIDs[j.ID] = true
		}
		for _, j := range s.ripEngine.QueuedJobs() {
			engineIDs[j.ID] = true
		}
	}

	offset := (page - 1) * activityHistoryPageSize
	dbJobs, err := s.store.ListAllJobs(activityHistoryPageSize+1, offset)
	if err != nil {
		slog.Error("failed to list history jobs", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load job history.")
	}

	store.HasMore = len(dbJobs) > activityHistoryPageSize
	if store.HasMore {
		dbJobs = dbJobs[:activityHistoryPageSize]
	}

	for _, j := range dbJobs {
		if engineIDs[j.ID] {
			continue
		}
		meta := parseTrackMetadata(j.TrackMetadata)
		store.History = append(store.History, activityJobJSON{
			ID:                j.ID,
			DiscName:          j.DiscName,
			TitleName:         j.TitleName,
			ContentType:       normalizeContentType(j.ContentType),
			Status:            j.Status,
			Error:             j.ErrorMessage,
			OutputPath:        j.OutputPath,
			Duration:          j.Duration,
			CreatedAt:         j.CreatedAt.Format("2006-01-02 15:04"),
			Salvageable:       salvageable(j.Status, j.ErrorMessage),
			SalvageNote:       j.SalvageNote,
			SizeHuman:         deliveredSize(j.OutputSizeBytes, meta.SizeHuman),
			AudioTracks:       meta.AudioTracks,
			SubtitleLanguages: meta.SubtitleLanguages,
		})
	}

	if s.orchestrator != nil {
		store.DiscsWithBackup = s.orchestrator.DiscsWithBackup()
	}

	// A page that lost its event stream has no way to catch up: events are
	// delivered once and never replayed. This is the same store the render
	// embeds, served so the page can ask again on reconnect.
	if wantsJSON(c) {
		return c.JSON(http.StatusOK, store)
	}

	storeBytes, err := json.Marshal(store)
	if err != nil {
		slog.Error("failed to marshal activity store", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to build activity data.")
	}

	return templates.Activity(templates.ActivityPageData{
		StoreJSON: string(storeBytes),
		Flash:     truncateFlash(c),
	}).Render(c.Request().Context(), c.Response().Writer)
}

// handleActivityCancel cancels an active job or removes a queued job.
func (s *Server) handleActivityCancel(c echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid job id")
	}

	if s.ripEngine == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "rip engine not available")
	}

	// Try removing from queue first (cheaper).
	if s.ripEngine.RemoveQueued(id) {
		return c.JSON(http.StatusOK, map[string]string{"status": "removed"})
	}

	// Try cancelling active job.
	if s.ripEngine.CancelActive(id) {
		return c.JSON(http.StatusOK, map[string]string{"status": "cancelled"})
	}

	return echo.NewHTTPError(http.StatusNotFound, "job not found in active or queued")
}

// activeAndQueuedJobIDs returns the DB IDs of all currently active and queued
// rip jobs. Used to exclude in-flight jobs from bulk history delete operations.
func (s *Server) activeAndQueuedJobIDs() []int64 {
	if s.ripEngine == nil {
		return nil
	}
	var ids []int64
	for _, j := range s.ripEngine.ActiveJobs() {
		ids = append(ids, j.ID)
	}
	for _, j := range s.ripEngine.QueuedJobs() {
		ids = append(ids, j.ID)
	}
	return ids
}

// handleActivityClearHistory deletes all rip jobs from the DB that are not
// currently active or queued in the rip engine.
func (s *Server) handleActivityClearHistory(c echo.Context) error {
	excludeIDs := s.activeAndQueuedJobIDs()

	if err := s.store.DeleteJobsExcept(excludeIDs); err != nil {
		slog.Error("failed to clear job history", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to clear history.")
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// handleActivityClearFiltered deletes rip jobs matching the provided search
// and status filters, excluding any jobs currently active or queued in the
// rip engine.
func (s *Server) handleActivityClearFiltered(c echo.Context) error {
	var req struct {
		Search string `json:"search"`
		Status string `json:"status"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	if req.Search == "" && (req.Status == "" || req.Status == "all") {
		return echo.NewHTTPError(http.StatusBadRequest, "at least one filter is required")
	}

	excludeIDs := s.activeAndQueuedJobIDs()

	if err := s.store.DeleteJobsByFilter(req.Search, req.Status, excludeIDs); err != nil {
		slog.Error("failed to clear filtered job history", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to clear history.")
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// deliveredSize reports the size of the file that actually landed, falling back
// to MakeMKV's estimate for jobs that predate the measurement.
//
// The estimate describes the title on the disc. Shown against a finished rip it
// claimed a 67.4 GB success for a file that was 118 MB, because nothing had
// ever looked at the result.
func deliveredSize(outputBytes int64, estimate string) string {
	if outputBytes <= 0 {
		return estimate
	}
	const unit = 1000
	if outputBytes < unit {
		return fmt.Sprintf("%d B", outputBytes)
	}
	div, exp := int64(unit), 0
	for n := outputBytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(outputBytes)/float64(div), "kMGTPE"[exp])
}

// salvageable reports whether a failure is one a salvage could get past.
//
// The signature is a rip that read the disc and came away with nothing, which
// is what a scratch does: MakeMKV abandons the title rather than writing a file
// with a gap in it. Offering salvage for every failure would put a two-hour
// operation in front of a user whose real problem is a full disk.
func salvageable(status, errMessage string) bool {
	if status != "failed" {
		return false
	}
	msg := strings.ToLower(errMessage)
	for _, sign := range []string{
		"saved no titles",   // makemkvcon copied nothing and said so
		"could not read it", // our own phrasing of the same
		"no .mkv file found",
	} {
		if strings.Contains(msg, sign) {
			return true
		}
	}
	return false
}

// salvageResumable reports whether this disc has a half-finished salvage that
// would be continued rather than started over.
func (s *Server) salvageResumable(status, errMessage, discName string) bool {
	if !salvageable(status, errMessage) || s.orchestrator == nil {
		return false
	}
	return s.orchestrator.SalvageResumable(discName)
}

// handleActivityDiscardBackup deletes the repaired copy a history entry came
// from, identified by disc rather than by drive.
//
// The drive page can discard by index because the drive is right there. A
// history row cannot: it may name a rip from a drive that has since been
// renumbered or unplugged, and acting on a stale index would delete some other
// disc's copy.
func (s *Server) handleActivityDiscardBackup(c echo.Context) error {
	if s.orchestrator == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "orchestrator not configured")
	}

	disc := c.FormValue("disc")
	if disc == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "no disc named")
	}

	if err := s.orchestrator.DiscardBackupForDisc(disc); err != nil {
		slog.Warn("discard disc copy failed", "disc", disc, "error", err)
		return discardHTTPError(err)
	}

	slog.Info("discarded disc copy on request from activity", "disc", disc)
	return c.JSON(http.StatusOK, map[string]any{"status": "discarded", "disc": disc})
}

// salvageNoteForJob returns the salvage note recorded against a running job.
//
// Active jobs come from the engine, which does not carry the note; it lives on
// the database record written when the job was created.
func (s *Server) salvageNoteForJob(id int64) string {
	if s.store == nil {
		return ""
	}
	job, err := s.store.GetJob(id)
	if err != nil || job == nil {
		return ""
	}
	return job.SalvageNote
}
