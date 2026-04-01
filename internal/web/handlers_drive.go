package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/db"
	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
	"github.com/johnpostlethwait/bluforge/templates"
)

// parseDriveIndex extracts and validates the ":id" path parameter as an int.
func parseDriveIndex(c echo.Context) (int, error) {
	return strconv.Atoi(c.Param("id"))
}

// handleDriveDetail renders the detail page for a single drive.
func (s *Server) handleDriveDetail(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	drv := s.driveMgr.GetDrive(idx)
	if drv == nil {
		return echo.NewHTTPError(http.StatusNotFound, "drive not found")
	}

	data := templates.DriveDetailData{
		DriveIndex: idx,
		DriveName:  drv.DriveName(),
		DiscName:   drv.DiscName(),
		State:      string(drv.State()),
		CSRFToken:  csrfToken(c),
	}

	// Build Alpine store hydration JSON.
	driveStore := DriveStoreJSON{
		DriveIndex:    idx,
		DriveName:     drv.DriveName(),
		DiscName:      drv.DiscName(),
		State:         string(drv.State()),
		CurrentStep:   1,
		Titles:        make([]TitleJSON, 0),
		SearchResults: make([]SearchResultJSON, 0),
		RipJobs:       make([]RipJobJSON, 0),
	}

	// Check for an existing disc mapping (from a previous rip of this disc).
	if s.orchestrator != nil && s.store != nil {
		if scan := s.orchestrator.GetCachedScanByDrive(idx); scan != nil {
			discKey := discdb.BuildDiscKey(scan)
			if discKey != "" {
				if mapping, err := s.store.GetMapping(discKey); err == nil && mapping != nil {
					driveStore.HasMapping = true
					driveStore.MatchedMedia = mapping.MediaTitle
					if mapping.MediaYear != "" {
						driveStore.MatchedMedia += " (" + mapping.MediaYear + ")"
					}
					driveStore.MatchedRelease = mapping.ReleaseID
				}
			}
		}
	}

	// Hydrate from drive session if available.
	if session := s.driveSessions.Get(idx); session != nil {
		driveStore.SelectedRelease = &SelectedReleaseJSON{
			MediaItemID: session.MediaItemID,
			ReleaseID:   session.ReleaseID,
			Title:       session.MediaTitle,
			Year:        session.MediaYear,
			Type:        session.MediaType,
		}
		driveStore.SearchResults = session.SearchResults
		if driveStore.SearchResults == nil {
			driveStore.SearchResults = make([]SearchResultJSON, 0)
		}

		// If both a cached scan and selected release exist, hydrate with
		// enriched titles so match data survives page refreshes.
		if session.ReleaseID != "" && session.RawSearchResults != nil && s.orchestrator != nil {
			if scan := s.orchestrator.GetCachedScanByDrive(idx); scan != nil {
				if disc := findDiscForRelease(session.RawSearchResults, session.ReleaseID); disc != nil {
					driveStore.Titles = enrichTitlesWithMatches(scan, *disc)
				}
			}
		}
	}

	// Compute the wizard step based on current state.
	// Step 1: Search, Step 2: Select Release, Step 3: Scan, Step 4: Review Titles, Step 5: Rip
	if s.ripEngine != nil && s.ripEngine.IsActive(idx) {
		driveStore.CurrentStep = 5
		// Hydrate active and queued jobs for this drive so Step 5 renders correctly on page load.
		driveStore.RipJobs = make([]RipJobJSON, 0)
		for _, j := range s.ripEngine.ActiveJobs() {
			if j.DriveIndex == idx {
				driveStore.RipJobs = append(driveStore.RipJobs, ripJobToJSON(j))
			}
		}
		for _, j := range s.ripEngine.QueuedJobs() {
			if j.DriveIndex == idx {
				driveStore.RipJobs = append(driveStore.RipJobs, ripJobToJSON(j))
			}
		}
	} else if len(driveStore.Titles) > 0 {
		driveStore.CurrentStep = 4
	} else if driveStore.SelectedRelease != nil && driveStore.SelectedRelease.ReleaseID != "" {
		driveStore.CurrentStep = 3
	} else if len(driveStore.SearchResults) > 0 {
		driveStore.CurrentStep = 2
	} else {
		driveStore.CurrentStep = 1
	}

	storeBytes, err := json.Marshal(driveStore)
	if err != nil {
		slog.Error("failed to marshal drive store", "error", err)
	}
	data.StoreJSON = string(storeBytes)

	// Check for error flash. Truncate to prevent abuse via crafted URLs.
	// Templ auto-escapes the output, but limiting length reduces phishing surface.
	if errMsg := c.QueryParam("error"); errMsg != "" {
		if len(errMsg) > 200 {
			errMsg = errMsg[:200]
		}
		data.Error = errMsg
	}

	return templates.DriveDetail(data).Render(c.Request().Context(), c.Response().Writer)
}

// handleDriveSearch executes a TheDiscDB search and returns the results as JSON.
func (s *Server) handleDriveSearch(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	query := strings.TrimSpace(c.FormValue("query"))
	searchType := c.FormValue("search_type")

	var items []discdb.MediaItem
	var searchErr string

	if query != "" {
		items = s.searchDiscDB(c, searchType, query)
		if items == nil {
			searchErr = "Search failed — TheDiscDB may be unavailable. Please try again."
		}
	}

	// Return JSON response.
	if searchErr != "" {
		return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": searchErr})
	}
	jsonRows := mediaItemsToSearchJSON(items)
	s.driveSessions.SetSearchResults(idx, jsonRows)
	s.driveSessions.SetRawSearchResults(idx, items)
	return c.JSON(http.StatusOK, jsonRows)
}

// searchDiscDB performs a cached search against TheDiscDB API.
// Returns nil if the search fails or no client is configured.
func (s *Server) searchDiscDB(c echo.Context, searchType, query string) []discdb.MediaItem {
	ctx := c.Request().Context()
	cacheKey := searchType + ":" + query

	// Try cache first.
	var items []discdb.MediaItem
	if s.discdbCache != nil {
		if cached, err := s.discdbCache.Get(cacheKey); err == nil && cached != nil {
			if err := json.Unmarshal(cached, &items); err != nil {
				slog.WarnContext(ctx, "discdb cache unmarshal failed", "key", cacheKey, "error", err)
				items = nil
			}
		}
	}

	if items != nil {
		return items
	}

	if s.discdbClient == nil {
		return nil
	}

	// Cache miss — call API.
	var apiErr error
	switch searchType {
	case "upc":
		items, apiErr = s.discdbClient.SearchByUPC(ctx, query)
	case "asin":
		items, apiErr = s.discdbClient.SearchByASIN(ctx, query)
	default:
		items, apiErr = s.discdbClient.SearchByTitle(ctx, query)
	}

	if apiErr != nil {
		slog.ErrorContext(ctx, "discdb search failed", "type", searchType, "query", query, "error", apiErr)
		return nil
	}

	if s.discdbCache != nil {
		if data, err := json.Marshal(items); err == nil {
			_ = s.discdbCache.Set(cacheKey, data)
		}
	}

	return items
}

// handleDriveRip submits rip jobs for the selected titles and redirects to the queue.
func (s *Server) handleDriveRip(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	cfg := s.GetConfig()

	if err := c.Request().ParseForm(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid form data")
	}

	discName := c.FormValue("disc_name")

	// Build title selections from form.
	// Per-title hidden inputs provide match-specific data (season, episode, etc.)
	// while global hidden inputs provide the release-level defaults.
	var titles []workflow.TitleSelection
	for _, tv := range c.Request().Form["titles"] {
		titleIdx, err := strconv.Atoi(tv)
		if err != nil {
			continue
		}
		// Per-title fields override global fields when present.
		contentType := c.FormValue(fmt.Sprintf("title_content_type_%d", titleIdx))
		if contentType == "" {
			contentType = c.FormValue("content_type")
		}
		contentTitle := c.FormValue(fmt.Sprintf("title_content_title_%d", titleIdx))
		if contentTitle == "" {
			contentTitle = c.FormValue("content_title")
		}
		titles = append(titles, workflow.TitleSelection{
			TitleIndex:   titleIdx,
			TitleName:    c.FormValue(fmt.Sprintf("title_name_%d", titleIdx)),
			ContentType:  contentType,
			ContentTitle: contentTitle,
			Year:         c.FormValue("content_year"),
			Season:       c.FormValue(fmt.Sprintf("title_season_%d", titleIdx)),
			Episode:      c.FormValue(fmt.Sprintf("title_episode_%d", titleIdx)),
			SourceFile:   c.FormValue(fmt.Sprintf("title_source_file_%d", titleIdx)),
		})
	}

	if len(titles) == 0 {
		return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/drives/%d?error=%s", idx, url.QueryEscape("No titles selected")))
	}

	// Build disc key from cached scan (avoid triggering a full rescan).
	discKey := ""
	if scan := s.orchestrator.GetCachedScanByDrive(idx); scan != nil {
		discKey = discdb.BuildDiscKey(scan)
	}

	duplicateAction := c.FormValue("duplicate_action")
	if duplicateAction == "" {
		duplicateAction = cfg.DuplicateAction
	}

	params := workflow.ManualRipParams{
		DriveIndex:      idx,
		DiscName:        discName,
		DiscKey:         discKey,
		Titles:          titles,
		OutputDir:       cfg.OutputDir,
		DuplicateAction: duplicateAction,
		MediaItemID:     c.FormValue("media_item_id"),
		ReleaseID:       c.FormValue("release_id"),
		MediaTitle:      c.FormValue("content_title"),
		MediaYear:       c.FormValue("content_year"),
		MediaType:       c.FormValue("content_type"),
	}

	result := s.orchestrator.ManualRip(params)

	if result.HasErrors() {
		return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/drives/%d?error=%s", idx, url.QueryEscape(result.ErrorSummary())))
	}

	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/drives/%d", idx))
}

// handleDriveScan runs a disc scan synchronously and returns the titles as JSON.
func (s *Server) handleDriveScan(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	slog.Info("scan requested", "drive_index", idx)

	drv := s.driveMgr.GetDrive(idx)
	if drv == nil {
		slog.Warn("scan requested for unknown drive", "drive_index", idx)
		return echo.NewHTTPError(http.StatusNotFound, "drive not found")
	}

	if s.orchestrator == nil {
		slog.Error("scan requested but orchestrator not configured")
		return echo.NewHTTPError(http.StatusServiceUnavailable, "scanner not configured")
	}

	scan, scanErr := s.orchestrator.ScanDisc(c.Request().Context(), idx)
	if scanErr != nil {
		slog.Error("disc scan failed", "drive_index", idx, "error", scanErr)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("disc scan failed: %v", scanErr))
	}


	// Save disc mapping if a release was selected in the session.
	if session := s.driveSessions.Get(idx); session != nil && session.ReleaseID != "" && s.store != nil {
		discKey := discdb.BuildDiscKey(scan)
		if discKey != "" {
			if err := s.store.SaveMapping(db.DiscMapping{
				DiscKey:     discKey,
				MediaItemID: session.MediaItemID,
				ReleaseID:   session.ReleaseID,
				MediaTitle:  session.MediaTitle,
				MediaYear:   session.MediaYear,
				MediaType:   session.MediaType,
			}); err != nil {
				slog.Warn("failed to save disc mapping", "disc_key", discKey, "error", err)
			}
		}
	}

	// If a release is selected, enrich titles with DiscDB match data.
	var titles []TitleJSON
	if session := s.driveSessions.Get(idx); session != nil && session.ReleaseID != "" && session.RawSearchResults != nil {
		if disc := findDiscForRelease(session.RawSearchResults, session.ReleaseID); disc != nil {
			titles = enrichTitlesWithMatches(scan, *disc)
			slog.Info("scan completed with match enrichment", "drive_index", idx, "title_count", len(titles))
			return c.JSON(http.StatusOK, titles)
		}
	}

	titles = scanToTitleJSON(scan)
	slog.Info("scan completed", "drive_index", idx, "title_count", len(titles))
	return c.JSON(http.StatusOK, titles)
}

// handleDriveRescan clears any existing mapping for a drive and redirects back to the detail page.
func (s *Server) handleDriveRescan(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	if err := s.orchestrator.Rescan(c.Request().Context(), idx); err != nil {
		slog.Error("rescan failed", "error", err, "drive_index", idx)
	}

	return c.Redirect(http.StatusSeeOther, "/drives/"+strconv.Itoa(idx))
}

// handleDriveMatch runs title matching using the cached scan and selected
// release. Returns enriched TitleJSON. Used as a fallback when both scan and
// release exist but the inline trigger points didn't fire (e.g., page refresh).
func (s *Server) handleDriveMatch(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	session := s.driveSessions.Get(idx)
	if session == nil || session.ReleaseID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "no release selected")
	}

	if s.orchestrator == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "orchestrator not configured")
	}

	scan := s.orchestrator.GetCachedScanByDrive(idx)
	if scan == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "no cached scan — scan the disc first")
	}

	if session.RawSearchResults == nil {
		return echo.NewHTTPError(http.StatusBadRequest, "no search results cached — search first")
	}

	disc := findDiscForRelease(session.RawSearchResults, session.ReleaseID)
	if disc == nil {
		return echo.NewHTTPError(http.StatusNotFound, "release disc not found in search results")
	}

	titles := enrichTitlesWithMatches(scan, *disc)
	return c.JSON(http.StatusOK, titles)
}
