package web

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// selectRequest is the JSON body for POST /drives/:id/select.
type selectRequest struct {
	MediaItemID string `json:"mediaItemID"`
	ReleaseID   string `json:"releaseID"`
	Title       string `json:"title"`
	Year        string `json:"year"`
	Type        string `json:"type"`
}

// handleDriveSelectAlpine persists the user's release selection in the drive session.
func (s *Server) handleDriveSelectAlpine(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	drv := s.driveMgr.GetDrive(idx)
	if drv == nil {
		return echo.NewHTTPError(http.StatusNotFound, "drive not found")
	}

	var req selectRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	// Preserve existing search results if any.
	var existingResults []SearchResultJSON
	if existing := s.driveSessions.Get(idx); existing != nil {
		existingResults = existing.SearchResults
	}

	// Persist selection in drive session.
	s.driveSessions.Set(idx, &DriveSession{
		MediaItemID:   req.MediaItemID,
		ReleaseID:     req.ReleaseID,
		MediaTitle:    req.Title,
		MediaYear:     req.Year,
		MediaType:     req.Type,
		SearchResults: existingResults,
	})

	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
