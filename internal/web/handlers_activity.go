package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/workflow"
	"github.com/johnpostlethwait/bluforge/templates"
)

const activityHistoryPageSize = 50

// queueJobJSON is the Alpine store shape for a single queue/active job.
type queueJobJSON struct {
	ID          int64  `json:"id"`
	DiscName    string `json:"discName"`
	TitleName   string `json:"titleName"`
	ContentType string `json:"contentType"`
	Status      string `json:"status"`
	Progress    int    `json:"progress"`
	Error       string `json:"error,omitempty"`
	DriveIndex  int    `json:"driveIndex"`
	FinishedAt  string `json:"finishedAt,omitempty"`
}

// activityStoreJSON is the Alpine.store('activity') shape.
type activityStoreJSON struct {
	Active    []queueJobJSON      `json:"active"`
	Pending   []queueJobJSON      `json:"pending"`
	Completed []queueJobJSON      `json:"completed"`
	History   []activityHistoryJSON `json:"history"`
	Page      int                  `json:"page"`
	HasMore   bool                 `json:"hasMore"`
}

// activityHistoryJSON extends the job shape with history-specific fields.
type activityHistoryJSON struct {
	ID          int64  `json:"id"`
	DiscName    string `json:"discName"`
	TitleName   string `json:"titleName"`
	ContentType string `json:"contentType"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	OutputPath  string `json:"outputPath,omitempty"`
	Duration    string `json:"duration,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
}

func (s *Server) handleActivity(c echo.Context) error {
	store := activityStoreJSON{
		Active:    make([]queueJobJSON, 0),
		Pending:   make([]queueJobJSON, 0),
		Completed: make([]queueJobJSON, 0),
		History:   make([]activityHistoryJSON, 0),
		Page:      1,
	}

	// Active jobs from the rip engine.
	if s.ripEngine != nil {
		for _, j := range s.ripEngine.ActiveJobs() {
			store.Active = append(store.Active, queueJobJSON{
				ID:          j.ID,
				DiscName:    j.DiscName,
				TitleName:   j.TitleName,
				ContentType: j.ContentType,
				Status:      string(j.Status),
				Progress:    j.Progress,
				Error:       j.Error,
				DriveIndex:  j.DriveIndex,
			})
		}

		// Queued (pending) jobs.
		for _, j := range s.ripEngine.QueuedJobs() {
			store.Pending = append(store.Pending, queueJobJSON{
				ID:          j.ID,
				DiscName:    j.DiscName,
				TitleName:   j.TitleName,
				ContentType: j.ContentType,
				Status:      string(j.Status),
				DriveIndex:  j.DriveIndex,
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
		store.Completed = append(store.Completed, queueJobJSON{
			ID:          j.ID,
			DiscName:    j.DiscName,
			TitleName:   j.TitleName,
			ContentType: j.ContentType,
			Status:      j.Status,
			Progress:    j.Progress,
			Error:       j.ErrorMessage,
			DriveIndex:  j.DriveIndex,
			FinishedAt:  j.UpdatedAt.Format("Jan 2 15:04"),
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
		store.History = append(store.History, activityHistoryJSON{
			ID:          j.ID,
			DiscName:    j.DiscName,
			TitleName:   j.TitleName,
			ContentType: j.ContentType,
			Status:      j.Status,
			Error:       j.ErrorMessage,
			OutputPath:  j.OutputPath,
			Duration:    j.Duration,
			CreatedAt:   j.CreatedAt.Format("2006-01-02 15:04"),
		})
	}

	storeBytes, err := json.Marshal(store)
	if err != nil {
		slog.Error("failed to marshal activity store", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to build activity data.")
	}

	return templates.Activity(templates.ActivityPageData{
		StoreJSON: string(storeBytes),
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

// handleActivityRetry re-submits a failed job.
func (s *Server) handleActivityRetry(c echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid job id")
	}

	job, err := s.store.GetJob(id)
	if err != nil {
		slog.Error("failed to get job for retry", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load job.")
	}
	if job == nil {
		return echo.NewHTTPError(http.StatusNotFound, "job not found")
	}
	if job.Status != "failed" {
		return echo.NewHTTPError(http.StatusBadRequest, "only failed jobs can be retried")
	}

	cfg := s.GetConfig()

	// Re-create and submit the job through the orchestrator.
	result := s.orchestrator.ManualRip(workflow.ManualRipParams{
		DriveIndex:      job.DriveIndex,
		DiscName:        job.DiscName,
		Titles:          []workflow.TitleSelection{{TitleIndex: job.TitleIndex, TitleName: job.TitleName, ContentType: job.ContentType}},
		OutputDir:       cfg.OutputDir,
		DuplicateAction: cfg.DuplicateAction,
	})

	if result.HasErrors() {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": result.ErrorSummary()})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "retried"})
}
