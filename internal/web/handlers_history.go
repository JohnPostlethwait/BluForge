package web

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/templates"
)

const historyPageSize = 50

func (s *Server) handleHistory(c echo.Context) error {
	// Parse "page" query param, defaulting to 1.
	page := 1
	if p := c.QueryParam("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}
	if page > 1000 {
		page = 1000
	}

	offset := (page - 1) * historyPageSize

	dbJobs, err := s.store.ListAllJobs(historyPageSize+1, offset)
	if err != nil {
		slog.Error("failed to list history jobs", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load job history.")
	}

	hasMore := len(dbJobs) > historyPageSize
	if hasMore {
		dbJobs = dbJobs[:historyPageSize]
	}

	rows := make([]templates.HistoryRow, 0, len(dbJobs))
	for _, j := range dbJobs {
		rows = append(rows, templates.HistoryRow{
			ID:          j.ID,
			DiscName:    j.DiscName,
			TitleName:   j.TitleName,
			ContentType: j.ContentType,
			OutputPath:  j.OutputPath,
			Status:      j.Status,
			Duration:    j.Duration,
			SizeBytes:   j.SizeBytes,
			CreatedAt:   j.CreatedAt.Format("2006-01-02 15:04"),
		})
	}

	return templates.History(rows, page, hasMore).Render(c.Request().Context(), c.Response().Writer)
}
