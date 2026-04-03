package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/templates"
)

const activityHistoryPageSize = 50

// activityJobJSON is the Alpine store shape for any job in the activity view,
// covering active, pending, completed, and history states. Fields not relevant
// to a given state are omitted from JSON output via omitempty.
type activityJobJSON struct {
	ID                int64               `json:"id"`
	DiscName          string              `json:"discName"`
	TitleName         string              `json:"titleName"`
	ContentType       string              `json:"contentType"`
	Status            string              `json:"status"`
	Progress          int                 `json:"progress"`
	Error             string              `json:"error,omitempty"`
	DriveIndex        int                 `json:"driveIndex"`
	FinishedAt        string              `json:"finishedAt,omitempty"`
	SizeHuman         string              `json:"sizeHuman,omitempty"`
	Duration          string              `json:"duration,omitempty"`
	AudioTracks       []ripper.AudioTrack `json:"audioTracks,omitempty"`
	SubtitleLanguages []string            `json:"subtitleLanguages,omitempty"`
	OutputPath        string              `json:"outputPath,omitempty"`
	CreatedAt         string              `json:"createdAt,omitempty"`
}

// activityStoreJSON is the Alpine.store('activity') shape.
type activityStoreJSON struct {
	Active    []activityJobJSON `json:"active"`
	Pending   []activityJobJSON `json:"pending"`
	Completed []activityJobJSON `json:"completed"`
	History   []activityJobJSON `json:"history"`
	Page      int               `json:"page"`
	HasMore   bool              `json:"hasMore"`
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
			store.Active = append(store.Active, activityJobJSON{
				ID:                j.ID,
				DiscName:          j.DiscName,
				TitleName:         j.TitleName,
				ContentType:       normalizeContentType(j.ContentType),
				Status:            string(j.Status),
				Progress:          j.Progress,
				Error:             j.Error,
				DriveIndex:        j.DriveIndex,
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
				SizeHuman:         j.TrackMetadata.SizeHuman,
				Duration:          j.TrackMetadata.Duration,
				AudioTracks:       j.TrackMetadata.AudioTracks,
				SubtitleLanguages: j.TrackMetadata.SubtitleLanguages,
			})
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
			SizeHuman:         meta.SizeHuman,
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
			SizeHuman:         meta.SizeHuman,
			AudioTracks:       meta.AudioTracks,
			SubtitleLanguages: meta.SubtitleLanguages,
		})
	}

	storeBytes, err := json.Marshal(store)
	if err != nil {
		slog.Error("failed to marshal activity store", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to build activity data.")
	}

	flash := c.QueryParam("flash")
	if len(flash) > 200 {
		flash = flash[:200]
	}

	return templates.Activity(templates.ActivityPageData{
		StoreJSON: string(storeBytes),
		Flash:     flash,
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

// handleActivityClearHistory deletes all rip jobs from the DB that are not
// currently active or queued in the rip engine.
func (s *Server) handleActivityClearHistory(c echo.Context) error {
	var excludeIDs []int64
	if s.ripEngine != nil {
		for _, j := range s.ripEngine.ActiveJobs() {
			excludeIDs = append(excludeIDs, j.ID)
		}
		for _, j := range s.ripEngine.QueuedJobs() {
			excludeIDs = append(excludeIDs, j.ID)
		}
	}

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

	var excludeIDs []int64
	if s.ripEngine != nil {
		for _, j := range s.ripEngine.ActiveJobs() {
			excludeIDs = append(excludeIDs, j.ID)
		}
		for _, j := range s.ripEngine.QueuedJobs() {
			excludeIDs = append(excludeIDs, j.ID)
		}
	}

	if err := s.store.DeleteJobsByFilter(req.Search, req.Status, excludeIDs); err != nil {
		slog.Error("failed to clear filtered job history", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to clear history.")
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
