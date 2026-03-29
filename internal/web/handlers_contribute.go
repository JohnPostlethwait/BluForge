package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/templates"
)

// handleContribute renders the TheDiscDB contribution opt-in page for a drive.
func (s *Server) handleContribute(c echo.Context) error {
	index, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	drive := s.driveMgr.GetDrive(index)
	if drive == nil {
		return echo.NewHTTPError(http.StatusNotFound, "drive not found")
	}

	hasGitHub := s.cfg.GitHubClientID != "" && s.cfg.GitHubClientSecret != ""

	data := templates.ContributeData{
		DriveIndex: drive.Index(),
		DiscName:   drive.DiscName(),
		Titles:     []templates.ContributeTitleRow{},
		HasGitHub:  hasGitHub,
	}

	return templates.Contribute(data).Render(c.Request().Context(), c.Response().Writer)
}

// handleContributeSubmit handles a TheDiscDB contribution form submission.
// Full API integration is pending coordination with the TheDiscDB maintainer.
func (s *Server) handleContributeSubmit(c echo.Context) error {
	index, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	slog.Info("TheDiscDB contribution submission received", "drive_index", index)
	slog.Warn("TheDiscDB contribution API integration pending")

	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/drives/%d", index))
}
