package web

import (
	"net/http"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/labstack/echo/v4"
)

// selectRequest is the JSON body for POST /drives/:id/select.
type selectRequest struct {
	MediaItemID string `json:"mediaItemID"`
	ReleaseID   string `json:"releaseID"`
	DiscID      string `json:"discID"`
	Title       string `json:"title"`
	Year        string `json:"year"`
	Type        string `json:"type"`
	UPC         string `json:"upc"`
	ASIN        string `json:"asin"`
	RegionCode  string `json:"regionCode"`
	Locale      string `json:"locale"`
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
	var existingRawResults []discdb.MediaItem
	if existing := s.driveSessions.Get(idx); existing != nil {
		existingResults = existing.SearchResults
		existingRawResults = existing.RawSearchResults
	}

	// Persist selection in drive session.
	s.driveSessions.Set(idx, &DriveSession{
		MediaItemID:       req.MediaItemID,
		ReleaseID:         req.ReleaseID,
		DiscID:            req.DiscID,
		MediaTitle:        req.Title,
		MediaYear:         req.Year,
		MediaType:         req.Type,
		ReleaseUPC:        req.UPC,
		ReleaseASIN:       req.ASIN,
		ReleaseRegionCode: req.RegionCode,
		ReleaseLocale:     req.Locale,
		SearchResults:     existingResults,
		RawSearchResults:  existingRawResults,
	})

	// If a scan is cached for this drive, enrich titles with match data and
	// return them so the frontend updates the Titles table immediately.
	if s.orchestrator != nil {
		if scan := s.orchestrator.GetCachedScanByDrive(idx); scan != nil {
			session := s.driveSessions.Get(idx)
			if session != nil && session.RawSearchResults != nil {
				if disc := findDiscForRelease(session.RawSearchResults, req.ReleaseID, req.DiscID); disc != nil {
					titles := enrichTitlesWithMatches(scan, *disc)
					return c.JSON(http.StatusOK, map[string]interface{}{
						"status": "ok",
						"titles": titles,
					})
				}
			}
		}
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
