package web

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/templates"
)

// queueJobJSON is the Alpine store shape for a single queue job.
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

// queueStoreJSON is the Alpine.store('queue') shape.
type queueStoreJSON struct {
	Active    []queueJobJSON `json:"active"`
	Pending   []queueJobJSON `json:"pending"`
	Completed []queueJobJSON `json:"completed"`
}

func (s *Server) handleQueue(c echo.Context) error {
	store := queueStoreJSON{
		Active:    make([]queueJobJSON, 0),
		Pending:   make([]queueJobJSON, 0),
		Completed: make([]queueJobJSON, 0),
	}

	// Active jobs from the rip engine.
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

	// Queued (pending) jobs from the engine's per-drive queues.
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

	// Completed and failed jobs from the database.
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

	storeBytes, err := json.Marshal(store)
	if err != nil {
		slog.Error("failed to marshal queue store", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to build queue data.")
	}

	return templates.Queue(templates.QueuePageData{
		StoreJSON: string(storeBytes),
	}).Render(c.Request().Context(), c.Response().Writer)
}
