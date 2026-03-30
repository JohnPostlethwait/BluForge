package web

import (
	"context"
	"log/slog"
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

// selectResponse is the JSON response for POST /drives/:id/select.
type selectResponse struct {
	Scanning bool        `json:"scanning"`
	Titles   []TitleJSON `json:"titles"`
}

// handleDriveSelectAlpine persists the user's release selection in the drive session
// and triggers a disc scan if no cached scan exists. This is the new Alpine.js
// endpoint that returns JSON; the old handleDriveSelect in handlers_drive.go
// will be removed in a later task.
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

	resp := selectResponse{
		Titles: make([]TitleJSON, 0),
	}

	// Check for cached scan; trigger background scan if missing.
	if drv.DiscName() != "" && s.orchestrator != nil {
		scan := s.orchestrator.CachedScan(idx, drv.DiscName())
		if scan == nil {
			resp.Scanning = true
			go func() {
				bgCtx := context.Background()
				result, scanErr := s.orchestrator.ScanDisc(bgCtx, idx)
				if scanErr != nil {
					slog.Error("background disc scan failed", "drive_index", idx, "error", scanErr)
					return
				}
				// Publish scan-complete SSE event.
				titles := scanToTitleJSON(result)
				s.broadcastScanComplete(idx, titles)
			}()
		} else {
			resp.Titles = scanToTitleJSON(scan)
		}
	}

	return c.JSON(http.StatusOK, resp)
}
