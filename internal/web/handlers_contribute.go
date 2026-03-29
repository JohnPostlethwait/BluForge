package web

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/templates"
)

// handleContribute renders an informational page directing users to
// TheDiscDB's website for contributions.
func (s *Server) handleContribute(c echo.Context) error {
	idx, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	drv := s.driveMgr.GetDrive(idx)
	discName := ""
	if drv != nil {
		discName = drv.DiscName()
	}

	data := templates.ContributeData{
		DriveIndex: idx,
		DiscName:   discName,
	}

	return templates.Contribute(data).Render(c.Request().Context(), c.Response().Writer)
}
