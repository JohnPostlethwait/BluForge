package web

import (
	"github.com/johnpostlethwait/bluforge/templates"
	"github.com/labstack/echo/v4"
)

func (s *Server) handleQueue(c echo.Context) error {
	// Get active jobs from the rip engine.
	activeJobs := s.ripEngine.ActiveJobs()
	active := make([]templates.QueueJobRow, 0, len(activeJobs))
	for _, j := range activeJobs {
		active = append(active, templates.QueueJobRow{
			ID:         j.ID,
			DiscName:   j.DiscName,
			TitleName:  j.TitleName,
			Status:     string(j.Status),
			Progress:   j.Progress,
			Error:      j.Error,
			DriveIndex: j.DriveIndex,
		})
	}

	// Get pending jobs from the store.
	pendingJobs, err := s.store.ListJobsByStatus("pending")
	if err != nil {
		return err
	}
	pending := make([]templates.QueueJobRow, 0, len(pendingJobs))
	for _, j := range pendingJobs {
		pending = append(pending, templates.QueueJobRow{
			ID:          j.ID,
			DiscName:    j.DiscName,
			TitleName:   j.TitleName,
			ContentType: j.ContentType,
			Status:      j.Status,
			Progress:    j.Progress,
			Error:       j.ErrorMessage,
			DriveIndex:  j.DriveIndex,
		})
	}

	// Get completed and failed jobs from the store.
	completedJobs, err := s.store.ListJobsByStatus("completed")
	if err != nil {
		return err
	}
	failedJobs, err := s.store.ListJobsByStatus("failed")
	if err != nil {
		return err
	}
	allDone := append(completedJobs, failedJobs...)
	completed := make([]templates.QueueJobRow, 0, len(allDone))
	for _, j := range allDone {
		completed = append(completed, templates.QueueJobRow{
			ID:          j.ID,
			DiscName:    j.DiscName,
			TitleName:   j.TitleName,
			ContentType: j.ContentType,
			Status:      j.Status,
			Progress:    j.Progress,
			Error:       j.ErrorMessage,
			DriveIndex:  j.DriveIndex,
		})
	}

	return templates.Queue(active, pending, completed).Render(c.Request().Context(), c.Response().Writer)
}
