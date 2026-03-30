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
	var existingRawResults []discdb.MediaItem
	if existing := s.driveSessions.Get(idx); existing != nil {
		existingResults = existing.SearchResults
		existingRawResults = existing.RawSearchResults
	}

	// Persist selection in drive session.
	s.driveSessions.Set(idx, &DriveSession{
		MediaItemID:      req.MediaItemID,
		ReleaseID:        req.ReleaseID,
		MediaTitle:       req.Title,
		MediaYear:        req.Year,
		MediaType:        req.Type,
		SearchResults:    existingResults,
		RawSearchResults: existingRawResults,
	})

	// If a scan is cached for this drive, enrich titles with match data and
	// return them so the frontend updates the Titles table immediately.
	if s.orchestrator != nil {
		scan := s.orchestrator.GetCachedScanByDrive(idx)
		slog.Info("select: checking for cached scan", "drive_index", idx, "has_scan", scan != nil)
		if scan != nil {
			// Look up disc from raw search results (preserved above or from prior search).
			session := s.driveSessions.Get(idx)
			hasRaw := session != nil && session.RawSearchResults != nil
			rawCount := 0
			if hasRaw {
				rawCount = len(session.RawSearchResults)
			}
			slog.Info("select: checking raw search results",
				"drive_index", idx,
				"has_session", session != nil,
				"has_raw_results", hasRaw,
				"raw_result_count", rawCount,
				"release_id", req.ReleaseID,
			)
			if hasRaw {
				disc := findDiscForRelease(session.RawSearchResults, req.ReleaseID)
				slog.Info("select: disc lookup result",
					"drive_index", idx,
					"found_disc", disc != nil,
					"release_id", req.ReleaseID,
				)
				if disc != nil {
					slog.Info("select: disc details",
						"disc_id", disc.ID,
						"disc_name", disc.Name,
						"disc_title_count", len(disc.Titles),
					)
					titles := enrichTitlesWithMatches(scan, *disc)
					matchCount := 0
					for _, t := range titles {
						if t.Matched {
							matchCount++
						}
					}
					slog.Info("select: enrichment complete",
						"drive_index", idx,
						"title_count", len(titles),
						"matched_count", matchCount,
					)
					return c.JSON(http.StatusOK, map[string]interface{}{
						"status": "ok",
						"titles": titles,
					})
				}
			}
		}
	}

	slog.Info("select: returning without titles", "drive_index", idx)
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
