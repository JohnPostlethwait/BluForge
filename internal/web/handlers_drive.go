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
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
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

	cfg := s.GetConfig()

	data := templates.DriveDetailData{
		DriveIndex:      idx,
		DriveName:       drv.DriveName(),
		DiscName:        drv.DiscName(),
		State:           string(drv.State()),
		CSRFToken:       csrfToken(c),
		DuplicateAction: cfg.DuplicateAction,
	}

	// Build Alpine store hydration JSON.
	driveStore := DriveStoreJSON{
		DriveIndex:        idx,
		DriveName:         drv.DriveName(),
		DiscName:          drv.DiscName(),
		State:             string(drv.State()),
		CurrentStep:       1,
		Titles:            make([]TitleJSON, 0),
		SearchResults:     make([]SearchResultJSON, 0),
		AudioLanguages:    make([]LangOptionJSON, 0),
		SubtitleLanguages: make([]LangOptionJSON, 0),
		KeepForcedSubs:    cfg.KeepForcedSubtitles,
		KeepLossless:      cfg.KeepLosslessAudio,
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
					driveStore.MatchedDiscID = mapping.DiscID
				}
			}
		}
	}

	// Hydrate from drive session if available.
	if session := s.driveSessions.Get(idx); session != nil {
		if session.ReleaseID != "" {
			driveStore.SelectedRelease = &SelectedReleaseJSON{
				MediaItemID: session.MediaItemID,
				ReleaseID:   session.ReleaseID,
				DiscID:      session.DiscID,
				Title:       session.MediaTitle,
				Year:        session.MediaYear,
				Type:        session.MediaType,
				UPC:         session.ReleaseUPC,
				ASIN:        session.ReleaseASIN,
				RegionCode:  session.ReleaseRegionCode,
				Locale:      session.ReleaseLocale,
			}
		}
		driveStore.SearchResults = session.SearchResults
		if driveStore.SearchResults == nil {
			driveStore.SearchResults = make([]SearchResultJSON, 0)
		}

		// If both a cached scan and selected release exist, hydrate with
		// enriched titles so match data survives page refreshes.
		if session.ReleaseID != "" && session.RawSearchResults != nil && s.orchestrator != nil {
			if scan := s.orchestrator.GetCachedScanByDrive(idx); scan != nil {
				if disc := findDiscForRelease(session.RawSearchResults, session.ReleaseID, session.DiscID); disc != nil {
					driveStore.Titles = enrichTitlesWithMatches(scan, *disc)
				}
			}
		}
	}

	// Populate disc-level language aggregates and lossless flag from cached scan.
	if s.orchestrator != nil {
		if scan := s.orchestrator.GetCachedScanByDrive(idx); scan != nil {
			audioLangs, subLangs := extractDiscLanguages(scan, cfg.PreferredAudioLangs, cfg.PreferredSubtitleLangs)
			driveStore.AudioLanguages = audioLangs
			driveStore.SubtitleLanguages = subLangs
			driveStore.HasLosslessAudio = discHasLosslessAudio(scan)
		}
	}

	// Compute the wizard step based on current state.
	// Step 1: Search, Step 2: Select Release, Step 3: Scan, Step 4: Review Titles, Step 5: Rip
	if s.ripEngine != nil {
		for _, j := range s.ripEngine.ActiveJobs() {
			if j.DriveIndex == idx {
				driveStore.RipActive = true
				driveStore.ActiveJobCount++
			}
		}
	}
	if driveStore.RipActive {
		driveStore.CurrentStep = 5
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
	data.Error = truncateQueryParam(c, "error")

	return templates.DriveDetail(data).Render(c.Request().Context(), c.Response().Writer)
}

// handleDriveSearch executes a TheDiscDB search and returns the results as JSON.
func (s *Server) handleDriveSearch(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	query := normalizeSearchQuery(strings.TrimSpace(c.FormValue("query")))
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
			if err := s.discdbCache.Set(cacheKey, data); err != nil {
				slog.WarnContext(ctx, "failed to cache discdb results", "key", cacheKey, "err", err)
			}
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
		titles = append(titles, parseTitleSelection(c, titleIdx))
	}

	if len(titles) == 0 {
		return redirectDriveError(c, idx, "No titles selected")
	}

	// Build disc key from cached scan (avoid triggering a full rescan).
	discKey := ""
	cachedScan := s.orchestrator.GetCachedScanByDrive(idx)
	if cachedScan != nil {
		discKey = discdb.BuildDiscKey(cachedScan)
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
		DiscID:          c.FormValue("disc_id"),
		MediaTitle:      c.FormValue("content_title"),
		MediaYear:       c.FormValue("content_year"),
		MediaType:       c.FormValue("content_type"),
	}

	// Parse track selection from form.
	audioLangs := c.FormValue("audio_langs")
	subtitleLangs := c.FormValue("subtitle_langs")
	keepForcedSubs := c.FormValue("keep_forced_subs") == "true"
	keepLossless := c.FormValue("keep_lossless") == "true"

	params.SelectionOpts = makemkv.NewSelectionOpts(audioLangs, subtitleLangs, keepForcedSubs, keepLossless)

	// If no DiscDB match, ensure a contribution record exists for later submission.
	if cachedScan != nil && params.MediaItemID == "" {
		s.orchestrator.EnsureContributionRecord(cachedScan)
	}

	result := s.orchestrator.ManualRip(params)

	if result.HasErrors() {
		return redirectDriveError(c, idx, result.ErrorSummary())
	}

	return c.Redirect(http.StatusSeeOther, "/activity?flash=Rip+started+successfully")
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
				DiscID:      session.DiscID,
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
		if disc := findDiscForRelease(session.RawSearchResults, session.ReleaseID, session.DiscID); disc != nil {
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

	disc := findDiscForRelease(session.RawSearchResults, session.ReleaseID, session.DiscID)
	if disc == nil {
		return echo.NewHTTPError(http.StatusNotFound, "release disc not found in search results")
	}

	titles := enrichTitlesWithMatches(scan, *disc)
	return c.JSON(http.StatusOK, titles)
}

// parseTitleSelection builds a workflow.TitleSelection from per-title form fields,
// falling back to global form fields for content type and title when per-title
// values are absent.
func parseTitleSelection(c echo.Context, titleIdx int) workflow.TitleSelection {
	contentType := c.FormValue(fmt.Sprintf("title_content_type_%d", titleIdx))
	if contentType == "" {
		contentType = c.FormValue("content_type")
	}
	contentTitle := c.FormValue(fmt.Sprintf("title_content_title_%d", titleIdx))
	if contentTitle == "" {
		contentTitle = c.FormValue("content_title")
	}
	var audioTracks []ripper.AudioTrack
	if raw := c.FormValue(fmt.Sprintf("title_audio_tracks_%d", titleIdx)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &audioTracks); err != nil {
			slog.Warn("failed to parse title audio tracks from form", "title_index", titleIdx, "error", err)
		}
	}
	var subtitleLangs []string
	if raw := c.FormValue(fmt.Sprintf("title_subtitle_langs_%d", titleIdx)); raw != "" {
		if err := json.Unmarshal([]byte(raw), &subtitleLangs); err != nil {
			slog.Warn("failed to parse title subtitle langs from form", "title_index", titleIdx, "error", err)
		}
	}
	return workflow.TitleSelection{
		TitleIndex:   titleIdx,
		TitleName:    c.FormValue(fmt.Sprintf("title_name_%d", titleIdx)),
		ContentType:  contentType,
		ContentTitle: contentTitle,
		Year:         c.FormValue("content_year"),
		Season:       c.FormValue(fmt.Sprintf("title_season_%d", titleIdx)),
		Episode:      c.FormValue(fmt.Sprintf("title_episode_%d", titleIdx)),
		SourceFile:   c.FormValue(fmt.Sprintf("title_source_file_%d", titleIdx)),
		TrackMetadata: ripper.TrackMetadata{
			SizeBytes:         parseSizeBytes(c.FormValue(fmt.Sprintf("title_size_bytes_%d", titleIdx))),
			SizeHuman:         c.FormValue(fmt.Sprintf("title_size_human_%d", titleIdx)),
			Duration:          c.FormValue(fmt.Sprintf("title_duration_%d", titleIdx)),
			AudioTracks:       audioTracks,
			SubtitleLanguages: subtitleLangs,
		},
	}
}

// truncateQueryParam returns c.QueryParam(key) capped at 200 characters.
// Used for user-visible flash and error messages to limit phishing surface
// (Templ auto-escapes, but length still matters).
func truncateQueryParam(c echo.Context, key string) string {
	v := c.QueryParam(key)
	if len(v) > 200 {
		return v[:200]
	}
	return v
}

// truncateFlash returns c.QueryParam("flash") capped at 200 characters.
func truncateFlash(c echo.Context) string {
	return truncateQueryParam(c, "flash")
}

func redirectDriveError(c echo.Context, idx int, msg string) error {
	if msg != "" {
		return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/drives/%d?error=%s", idx, url.QueryEscape(msg)))
	}
	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/drives/%d", idx))
}

// normalizeSearchQuery cleans up a raw disc name or user-typed query:
// underscores and hyphens are replaced with spaces, and extra whitespace is collapsed.
func normalizeSearchQuery(q string) string {
	r := strings.NewReplacer("_", " ", "-", " ")
	return strings.Join(strings.Fields(r.Replace(q)), " ")
}

