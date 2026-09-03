package web

import (
	"log/slog"
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
	if existing, ok := s.driveSessions.Snapshot(idx); ok {
		existingResults = existing.SearchResults
		existingRawResults = existing.RawSearchResults
	}

	// Persist selection in drive session, bound to the disc it describes so a
	// later swap cannot leave it applying to whatever comes next.
	s.driveSessions.Set(idx, &DriveSession{
		DiscLabel:         drv.DiscName(),
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
			if session, ok := s.driveSessions.Snapshot(idx); ok && session.RawSearchResults != nil {
				titles := titlesForScan(scan, session.RawSearchResults, req.ReleaseID, req.DiscID)
				return c.JSON(http.StatusOK, map[string]interface{}{
					"status": "ok",
					"titles": titles,
				})
			}
		}
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// handleDriveClearMatch discards everything the drive remembers about what disc
// is in it: the release the user picked, the search that produced it, the
// cached title list, and any mapping saved by an earlier rip.
//
// A selection that turns out to be wrong was previously unremovable. It lived
// in the drive session, survived every refresh, and was only dropped on eject —
// so the page kept rebuilding itself around the wrong match, and re-scanning
// answered from a cache keyed on the disc the user was trying to stop trusting.
func (s *Server) handleDriveClearMatch(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	if drv := s.driveMgr.GetDrive(idx); drv == nil {
		return echo.NewHTTPError(http.StatusNotFound, "drive not found")
	}

	// The mapping is keyed on the cached scan, so it has to go first. Dropping
	// the scan destroys the only way to name the row, and the delete would then
	// quietly match nothing while still reporting success.
	if s.orchestrator != nil && s.store != nil {
		if scan := s.orchestrator.GetCachedScanByDrive(idx); scan != nil {
			if discKey := discdb.BuildDiscKey(scan); discKey != "" {
				if err := s.store.DeleteMapping(discKey); err != nil {
					slog.Error("clear match: delete mapping failed",
						"drive_index", idx, "disc_key", discKey, "error", err)
					return echo.NewHTTPError(http.StatusInternalServerError, "could not clear the saved match")
				}
			}
		}
	}

	s.driveSessions.Clear(idx)

	if s.orchestrator != nil {
		s.orchestrator.InvalidateScan(idx)
	}

	slog.Info("cleared disc match on request", "drive_index", idx)
	return c.JSON(http.StatusOK, map[string]string{"status": "cleared"})
}
