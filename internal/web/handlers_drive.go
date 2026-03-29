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

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/workflow"
	"github.com/johnpostlethwait/bluforge/templates"
)

// mediaItemsToRows flattens a slice of discdb.MediaItem into template SearchResultRows,
// one row per release across all items.
func mediaItemsToRows(items []discdb.MediaItem) []templates.SearchResultRow {
	var rows []templates.SearchResultRow
	for _, item := range items {
		for _, rel := range item.Releases {
			format := ""
			if len(rel.Discs) > 0 {
				format = rel.Discs[0].Format
			}
			rows = append(rows, templates.SearchResultRow{
				MediaTitle:   item.Title,
				MediaYear:    item.Year,
				MediaType:    item.Type,
				ReleaseTitle: rel.Title,
				ReleaseUPC:   rel.UPC,
				ReleaseASIN:  rel.ASIN,
				RegionCode:   rel.RegionCode,
				Format:       format,
				DiscCount:    len(rel.Discs),
				ReleaseID:    rel.ID,
				MediaItemID:  item.ID,
			})
		}
	}
	return rows
}

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
		DriveName:  drv.DevicePath(),
		DiscName:   drv.DiscName(),
		State:      string(drv.State()),
	}

	// Check for a remembered disc mapping.
	if drv.DiscName() != "" && s.store != nil {
		scan, scanErr := s.orchestrator.ScanDisc(c.Request().Context(), idx)
		if scanErr == nil && scan != nil {
			discKey := discdb.BuildDiscKey(scan)
			mapping, mappingErr := s.store.GetMapping(discKey)
			if mappingErr != nil {
				slog.WarnContext(c.Request().Context(), "failed to load disc mapping", "disc_key", discKey, "error", mappingErr)
			}
			if mapping != nil {
				data.HasMapping = true
				data.MatchedMedia = mapping.MediaTitle + " (" + mapping.MediaYear + ")"
				data.MatchedRelease = mapping.ReleaseID
			}

			// Populate title rows from scan.
			for _, t := range scan.Titles {
				data.Titles = append(data.Titles, templates.TitleRow{
					Index:      t.Index,
					Name:       t.Name(),
					Duration:   t.Duration(),
					Size:       t.SizeHuman(),
					SourceFile: t.SourceFile(),
					Selected:   true,
				})
			}
		}
	}

	// Check for error flash.
	if errMsg := c.QueryParam("error"); errMsg != "" {
		data.Error = errMsg
	}

	return templates.DriveDetail(data).Render(c.Request().Context(), c.Response().Writer)
}

// handleDriveSearch executes a TheDiscDB search and returns the results partial.
func (s *Server) handleDriveSearch(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	query := strings.TrimSpace(c.FormValue("query"))
	searchType := c.FormValue("search_type")

	var rows []templates.SearchResultRow
	var searchErr string

	if query != "" {
		ctx := c.Request().Context()
		cacheKey := searchType + ":" + query

		// Try cache first.
		var items []discdb.MediaItem
		if s.discdbCache != nil {
			if cached, err := s.discdbCache.Get(cacheKey); err == nil && cached != nil {
				if err := json.Unmarshal(cached, &items); err != nil {
					slog.WarnContext(ctx, "discdb cache unmarshal failed", "key", cacheKey, "error", err)
					items = nil // fall through to API
				}
			}
		}

		// Cache miss — call API.
		if items == nil {
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
				searchErr = "Search failed — TheDiscDB may be unavailable. Please try again."
			} else if s.discdbCache != nil {
				if data, err := json.Marshal(items); err == nil {
					_ = s.discdbCache.Set(cacheKey, data)
				}
			}
		}

		if items != nil {
			rows = mediaItemsToRows(items)
		}
	}

	return templates.DriveSearchResults(idx, rows, searchErr).Render(c.Request().Context(), c.Response().Writer)
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
	var titles []workflow.TitleSelection
	for _, tv := range c.Request().Form["titles"] {
		titleIdx, err := strconv.Atoi(tv)
		if err != nil {
			continue
		}
		titles = append(titles, workflow.TitleSelection{
			TitleIndex:   titleIdx,
			TitleName:    c.FormValue(fmt.Sprintf("title_name_%d", titleIdx)),
			ContentType:  c.FormValue("content_type"),
			ContentTitle: c.FormValue("content_title"),
			Year:         c.FormValue("content_year"),
		})
	}

	if len(titles) == 0 {
		return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/drives/%d?error=%s", idx, url.QueryEscape("No titles selected")))
	}

	// Build disc key if we have scan data.
	discKey := ""
	if scan, err := s.orchestrator.ScanDisc(c.Request().Context(), idx); err == nil {
		discKey = discdb.BuildDiscKey(scan)
	}

	params := workflow.ManualRipParams{
		DriveIndex:      idx,
		DiscName:        discName,
		DiscKey:         discKey,
		Titles:          titles,
		OutputDir:       cfg.OutputDir,
		MovieTemplate:   cfg.MovieTemplate,
		SeriesTemplate:  cfg.SeriesTemplate,
		DuplicateAction: cfg.DuplicateAction,
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

	return c.Redirect(http.StatusSeeOther, "/queue")
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
