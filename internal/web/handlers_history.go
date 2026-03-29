package web

import (
	"strconv"

	"github.com/johnpostlethwait/bluforge/templates"
	"github.com/labstack/echo/v4"
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

	offset := (page - 1) * historyPageSize

	// Fetch one extra row to detect whether there is a next page.
	dbJobs, err := s.store.ListAllJobs(historyPageSize+1, offset)
	if err != nil {
		return err
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
