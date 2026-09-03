package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
	"github.com/johnpostlethwait/bluforge/templates"
)

// langCodeRe matches valid ISO 639-2 language codes: exactly 3 lowercase ASCII letters.
var langCodeRe = regexp.MustCompile(`^[a-z]{3}$`)

// validateLangCodes checks that every non-empty code in the comma-separated raw
// string matches the ISO 639-2 format (exactly 3 lowercase letters). label is
// used in the returned error message (e.g. "audio language").
func validateLangCodes(raw, label string) error {
	for _, code := range strings.Split(raw, ",") {
		c := strings.TrimSpace(code)
		if c != "" && !langCodeRe.MatchString(c) {
			return fmt.Errorf("invalid %s code %q: must be a 3-letter ISO 639-2 code", label, c)
		}
	}
	return nil
}

// parseDriveIndex extracts and validates the ":id" path parameter as an int.
func parseDriveIndex(c echo.Context) (int, error) {
	return strconv.Atoi(c.Param("id"))
}

// buildDriveStore assembles the drive page's client-side state.
//
// The page render and the state endpoint both use it, so a client that has
// lost its event stream and resyncs sees exactly what a fresh page load would.
func (s *Server) buildDriveStore(idx int, drv *drivemanager.DriveStateMachine) DriveStoreJSON {
	cfg := s.GetConfig()

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

		PreferredAudioLangs:    splitLangCodesSlice(cfg.PreferredAudioLangs),
		PreferredSubtitleLangs: splitLangCodesSlice(cfg.PreferredSubtitleLangs),
	}

	// Say what is in the drive before asking anything about it.
	//
	// Everything below looks things up by drive index — the cached scan, the
	// repaired copy — and both are really keyed by disc. The orchestrator learns
	// the disc from events, and an event can be missed or simply not have
	// happened yet after a restart. This is the same fact taken from the drive
	// itself, so the answers below are right even then.
	if s.orchestrator != nil {
		s.orchestrator.SetDriveDisc(idx, drv.DiscName())
	}

	// Drop a session that belongs to a disc which is no longer in the drive.
	//
	// The release, the search behind it and the titles named from it all
	// describe one disc. Clearing them was left to events, and both of them miss
	// an ordinary swap: an eject is only believed after the drive reports empty
	// for a continuous 30s, which taking one disc out and putting the next in
	// never reaches, and the orchestrator's disc-changed callback fires only
	// from a scan that has a previous scan to compare against — which the insert
	// has just invalidated. Ripping Akira and loading the next disc showed
	// "Matched: Akira (1988)" on that disc's page.
	//
	// Asking the drive is what the block above already does, for the same
	// reason, and it holds whether or not any event arrived.
	if session, ok := s.driveSessions.Snapshot(idx); ok &&
		session.DiscLabel != "" && drv.DiscName() != "" &&
		session.DiscLabel != drv.DiscName() {
		slog.Info("dropping a drive session bound to a disc that is no longer in the drive",
			"drive_index", idx, "session_disc", session.DiscLabel, "drive_disc", drv.DiscName())
		s.driveSessions.Clear(idx)
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
	//
	// Through Snapshot rather than Get: Get hands out the live session, and the
	// search handlers write SearchResults, RawSearchResults and DiscLabel into
	// that same object. One tab searching while another renders the page is
	// enough to be reading a field mid-write.
	if session, ok := s.driveSessions.Snapshot(idx); ok {
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

	// Say when the cached scan was taken, so the page can mark a title list it
	// did not just read out of the drive.
	if s.orchestrator != nil {
		if info := s.orchestrator.CachedScanInfo(idx); info != nil {
			driveStore.ScanCachedAt = info.CachedAt.Unix()
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

	// Surface what MakeMKV complained about during the scan. A disc with
	// unreadable sectors yields a shorter title list and nothing else to show
	// for it, which reads as success.
	if s.orchestrator != nil {
		driveStore.SalvageActive = s.orchestrator.SalvageInProgress(idx)
	}

	if s.orchestrator != nil {
		if st := s.orchestrator.ScanStatus(idx); st.Active {
			driveStore.ScanActive = true
			driveStore.ScanOperation = st.Operation
			driveStore.ScanStartedAt = st.StartedAt.Unix()
		}
	}

	driveStore.ScanOutput = make([]makemkv.ScanWarning, 0)
	driveStore.ScanDiagnosis = makemkv.ScanDiagnosis{Findings: []makemkv.ScanFinding{}}
	if s.orchestrator != nil {
		if scan := s.orchestrator.GetCachedScanByDrive(idx); scan != nil {
			driveStore.ScanOutput = makemkv.ScanOutput(scan.Messages)
			driveStore.ScanDiagnosis = makemkv.Diagnose(scan.Messages)
		}
	}

	// Recovery state comes from the orchestrator rather than the event stream,
	// so a reconnecting client can clear a banner left up by a lost "done".
	if s.orchestrator != nil {
		driveStore.RecoveryActive = s.orchestrator.RecoveryInProgress(idx)
		driveStore.HasBackup = s.orchestrator.RecoveredDir(idx) != ""
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

	return driveStore
}

// handleDriveState returns the drive page's state as JSON.
//
// The page previously trusted the SSE stream as its only source of truth, with
// no replay and no resync. A client whose connection dropped — a laptop
// sleeping mid-backup was enough — kept whatever it last heard forever, showing
// "copying, 94%" long after the work had finished. This gives it somewhere
// authoritative to ask on reconnect.
func (s *Server) handleDriveState(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	drv := s.driveMgr.GetDrive(idx)
	if drv == nil {
		return echo.NewHTTPError(http.StatusNotFound, "drive not found")
	}

	return c.JSON(http.StatusOK, s.buildDriveStore(idx, drv))
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

	driveStore := s.buildDriveStore(idx, drv)

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
	// Bind the results to the disc they were searched for, so a swap does not
	// leave the previous disc's candidate list on the new disc's page.
	if drv := s.driveMgr.GetDrive(idx); drv != nil {
		s.driveSessions.SetDiscLabel(idx, drv.DiscName())
	}
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

	// A negative result is not cached.
	//
	// "Not in TheDiscDB" is the answer most likely to change — someone
	// contributes the disc, quite possibly this user, from this app. Cached
	// alongside real answers it held for the full TTL, so the search kept
	// saying no such release with no way to make it look again.
	if s.discdbCache != nil && len(items) > 0 {
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

	if err := validateLangCodes(c.FormValue("audio_langs"), "audio language"); err != nil {
		return redirectDriveError(c, idx, err.Error())
	}
	if err := validateLangCodes(c.FormValue("subtitle_langs"), "subtitle language"); err != nil {
		return redirectDriveError(c, idx, err.Error())
	}

	// The disc the page was built from has to still be the disc in the drive.
	//
	// This name arrives in the form, and it used to be taken on trust — along
	// with the cached scan and the release selection built around it. Swapping
	// discs between rendering the page and pressing Rip, which is what working
	// through a box set looks like, ripped the new disc into the previous
	// disc's filenames and saved a mapping asserting it was that film. Nothing
	// downstream could notice: every identifier in play described the disc that
	// had just come out.
	//
	// Only a disagreement counts. A disc with no volume label, or a form that
	// carries no name, cannot be compared and is not evidence of a swap.
	discName := c.FormValue("disc_name")
	if drv := s.driveMgr.GetDrive(idx); drv != nil {
		inDrive := drv.DiscName()
		if discName != "" && inDrive != "" && discName != inDrive {
			slog.Warn("refusing a rip for a disc that is no longer in the drive",
				"drive_index", idx, "form_disc", discName, "drive_disc", inDrive)
			return redirectDriveError(c, idx, fmt.Sprintf(
				"%s is no longer in the drive — %s is. Re-scan before ripping.", discName, inDrive))
		}
		// The drive is the authority on what it holds, so prefer its name when
		// the form did not carry one.
		if discName == "" {
			discName = inDrive
		}
	}

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

// handleDriveScan returns the titles for a disc, starting the scan in the
// background if it has not run yet.
//
// It used to block until makemkvcon exited. A disc that retries unreadable
// sectors can take the better part of an hour, which is longer than any browser
// will wait — and the request dying killed the scan with it.
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

	// Always a read of the disc, never the cache.
	//
	// This used to answer from cache when there was one, which is how a two-disc
	// set whose discs share a volume label served the first disc's titles for the
	// second: the cache is keyed on that label, and nothing else about the discs
	// was ever compared. Pressing Scan is a request to find out what is in the
	// drive, and only reading it can answer that.
	//
	// Fetching the titles of a scan that already finished is a different request
	// — GET on this path — so this costs nothing the user did not ask for.
	if err := s.orchestrator.StartRescan(idx); err != nil && !errors.Is(err, workflow.ErrScanInProgress) {
		slog.Error("could not start disc scan", "drive_index", idx, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("disc scan failed: %v", err))
	}

	// Progress, completion and failure all arrive as disc_scan SSE events.
	return c.JSON(http.StatusAccepted, map[string]any{
		"status":  "scanning",
		"message": "Reading the disc. This can take a while on a damaged disc; progress is shown on this page.",
	})
}

// ScanResultJSON is the stored result of the most recent scan of a drive.
//
// Everything this endpoint serves comes from the cache — that is what it is
// for — so there is no boolean saying so. CachedAt is the useful fact: when the
// disc was actually read. A title list read a moment ago and one cached before
// the disc was swapped are otherwise identical, and the caller decides what to
// make of the age.
type ScanResultJSON struct {
	Titles   []TitleJSON `json:"titles"`
	DiscName string      `json:"disc_name"`
	// CachedAt is when the scan was taken, as a Unix timestamp.
	CachedAt int64 `json:"cached_at"`
}

// handleDriveScanResult returns the titles of a scan that has already run.
//
// This is what the page fetches when it sees the done event, and what it uses
// to render a drive it is returning to. It may use the cache — and says so, so
// the page never presents a cached list as a fresh read of the disc.
func (s *Server) handleDriveScanResult(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	if s.driveMgr.GetDrive(idx) == nil {
		return echo.NewHTTPError(http.StatusNotFound, "drive not found")
	}
	if s.orchestrator == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "scanner not configured")
	}

	// No scan has run for this drive. Not an error: the page asks before the
	// first scan, and an empty body is how it learns there is nothing yet.
	info := s.orchestrator.CachedScanInfo(idx)
	if info == nil || info.Scan == nil {
		return c.NoContent(http.StatusNoContent)
	}
	scan := info.Scan

	// No mapping is written here.
	//
	// This is a GET — the page fetching the titles of a scan that has finished —
	// and it used to upsert disc_mappings whenever a release happened to be
	// selected. Searching, picking a release and scanning is browsing, not
	// confirming: it taught BluForge that this disc is that film without the
	// user ever pressing Rip, so the page greeted the disc with "Previously
	// matched" ever after and auto-rip acted on it. The row it wrote also
	// carried no disc_name, blanking the column a real rip had filled in.
	//
	// ManualRip saves the mapping, at the point the user commits to the match.

	// If a release is selected, enrich titles with DiscDB match data.
	var titles []TitleJSON
	if session, ok := s.driveSessions.Snapshot(idx); ok && session.ReleaseID != "" && session.RawSearchResults != nil {
		if disc := findDiscForRelease(session.RawSearchResults, session.ReleaseID, session.DiscID); disc != nil {
			titles = enrichTitlesWithMatches(scan, *disc)
		}
	}
	if titles == nil {
		titles = scanToTitleJSON(scan)
	}

	slog.Info("scan results served", "drive_index", idx,
		"title_count", len(titles), "cached_at", info.CachedAt)

	return c.JSON(http.StatusOK, ScanResultJSON{
		Titles:   titles,
		DiscName: scan.DiscName,
		CachedAt: info.CachedAt.Unix(),
	})
}

// handleDriveSalvage starts a salvage of a physically damaged disc.
//
// Never automatic: a salvage produces a file MakeMKV would refuse to make,
// containing damaged video wherever the disc could not be read. The user is
// told what that means and chooses, and this is the endpoint that choice hits.
func (s *Server) handleDriveSalvage(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}
	if s.orchestrator == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "salvage not configured")
	}
	if s.driveMgr.GetDrive(idx) == nil {
		return echo.NewHTTPError(http.StatusNotFound, "drive not found")
	}

	// The drive number on a failed job was recorded when the rip ran. Optical
	// devices renumber -- a USB drive that re-enumerated moved Rambo from index
	// 0 to index 1 -- so salvaging by that number ran against an empty drive and
	// failed in four seconds. The disc is what identifies the work.
	if disc := c.QueryParam("disc"); disc != "" {
		if found, ok := s.driveHoldingDisc(disc); ok && found != idx {
			slog.Info("salvage: the disc has moved drives since the rip",
				"recorded_index", idx, "actual_index", found, "disc", disc)
			idx = found
		} else if !ok {
			return echo.NewHTTPError(http.StatusConflict,
				fmt.Sprintf("%s is not in any drive. Insert it and try again.", disc))
		}
	}

	slog.Info("salvage requested", "drive_index", idx)

	err = s.orchestrator.StartSalvage(idx)
	if errors.Is(err, workflow.ErrSalvageInProgress) {
		// Not a failure: the salvage the caller wanted is already running.
		return c.JSON(http.StatusAccepted, map[string]any{
			"status":  "salvaging",
			"message": "This disc is already being salvaged.",
		})
	}
	if err != nil {
		slog.Error("could not start salvage", "drive_index", idx, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusAccepted, map[string]any{
		"status": "salvaging",
		"message": "Copying what the drive can still read. This takes a few hours; " +
			"progress is shown on this page.",
	})
}

// handleDriveSalvagePause stops a running salvage without discarding what it
// has recovered.
//
// Presented as a pause because that is what it is from the outside: ddrescue's
// map survives, so starting again continues from the same place rather than
// re-reading the disc.
func (s *Server) handleDriveSalvagePause(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}
	if s.orchestrator == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "salvage not configured")
	}

	if !s.orchestrator.CancelSalvage(idx) {
		return c.JSON(http.StatusOK, map[string]any{
			"status":  "idle",
			"message": "No salvage was running.",
		})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"status":  "paused",
		"message": "Paused. Everything recovered so far is kept; resuming continues from here.",
	})
}

// driveHoldingDisc reports which drive currently holds the named disc.
//
// Drive numbers are not stable across sessions: they follow device enumeration,
// and a USB drive that reconnects can take a different one. Anything acting on
// a number recorded earlier has to check.
func (s *Server) driveHoldingDisc(discName string) (int, bool) {
	// An empty name would otherwise match every empty drive, which is how a
	// salvage would end up pointed at a drive with nothing in it.
	if discName == "" {
		return 0, false
	}
	for _, d := range s.driveMgr.GetAllDrives() {
		if d.DiscName() == discName {
			return d.Index(), true
		}
	}
	return 0, false
}

// discardHTTPError turns a discard failure into the status that describes it.
//
// Both discard endpoints used to answer every failure with 404. "A rip is
// reading this copy" is not "there is no such copy": the first tells the user
// to wait, the second sends them looking for something that is right there.
func discardHTTPError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, workflow.ErrBackupInUse) {
		return echo.NewHTTPError(http.StatusConflict, err.Error())
	}
	return echo.NewHTTPError(http.StatusNotFound, err.Error())
}

// handleDiscardBackup deletes a drive's recovery scratch copy on request.
//
// A copy is kept whenever a rip did not succeed, because re-reading the disc
// costs tens of minutes and MakeMKV may refuse to read it at all. Reclaiming
// the space — up to ~100GB — therefore has to be something the user can ask for.
func (s *Server) handleDiscardBackup(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}
	if s.orchestrator == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "orchestrator not configured")
	}

	dir := s.orchestrator.RecoveredDir(idx)
	if err := s.orchestrator.DiscardBackup(idx); err != nil {
		slog.Warn("discard backup failed", "drive_index", idx, "error", err)
		return discardHTTPError(err)
	}

	slog.Info("discarded disc backup on request", "drive_index", idx, "dir", dir)
	return c.JSON(http.StatusOK, map[string]any{
		"status": "discarded",
		"dir":    dir,
	})
}

// handleDriveMatch runs title matching using the cached scan and selected
// release. Returns enriched TitleJSON. Used as a fallback when both scan and
// release exist but the inline trigger points didn't fire (e.g., page refresh).
func (s *Server) handleDriveMatch(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	session, ok := s.driveSessions.Snapshot(idx)
	if !ok || session.ReleaseID == "" {
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
		TitleIndex: titleIdx,
		TitleName:  c.FormValue(fmt.Sprintf("title_name_%d", titleIdx)),
		// The file name the review page showed for this title, carried back so
		// the rip writes exactly what was shown rather than recomputing it.
		OutputName:   c.FormValue(fmt.Sprintf("title_output_name_%d", titleIdx)),
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
