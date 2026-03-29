package web

import (
	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/templates"
	"github.com/johnpostlethwait/bluforge/templates/components"
)

// handleDashboard renders the dashboard with all known drive cards.
func (s *Server) handleDashboard(c echo.Context) error {
	drives := s.driveMgr.GetAllDrives()

	cards := make([]components.DriveCardData, 0, len(drives))
	for _, dsm := range drives {
		cards = append(cards, components.DriveCardData{
			Index:    dsm.Index(),
			Name:     dsm.DevicePath(),
			DiscName: dsm.DiscName(),
			State:    string(dsm.State()),
		})
	}

	data := templates.DashboardData{Drives: cards}
	return templates.Dashboard(data).Render(c.Request().Context(), c.Response().Writer)
}
