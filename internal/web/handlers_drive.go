package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/ripper"
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

	dsm := s.driveMgr.GetDrive(idx)
	if dsm == nil {
		return echo.NewHTTPError(http.StatusNotFound, "drive not found")
	}

	data := templates.DriveDetailData{
		DriveIndex: dsm.Index(),
		DriveName:  dsm.DevicePath(),
		DiscName:   dsm.DiscName(),
		State:      string(dsm.State()),
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

	if query != "" {
		ctx := c.Request().Context()

		switch searchType {
		case "upc":
			results, err := s.discdbClient.SearchByUPC(ctx, query)
			if err == nil {
				rows = mediaItemsToRows(results)
			}
		case "asin":
			results, err := s.discdbClient.SearchByASIN(ctx, query)
			if err == nil {
				rows = mediaItemsToRows(results)
			}
		default:
			results, err := s.discdbClient.SearchByTitle(ctx, query)
			if err == nil {
				rows = mediaItemsToRows(results)
			}
		}
	}

	return templates.DriveSearchResults(idx, rows).Render(c.Request().Context(), c.Response().Writer)
}

// handleDriveRip submits rip jobs for the selected titles and redirects to the queue.
func (s *Server) handleDriveRip(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	dsm := s.driveMgr.GetDrive(idx)
	discName := ""
	if dsm != nil {
		discName = dsm.DiscName()
	}

	if err := c.Request().ParseForm(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid form data")
	}

	for _, tv := range c.Request().Form["titles"] {
		titleIdx, err := strconv.Atoi(tv)
		if err != nil {
			continue
		}
		job := ripper.NewJob(idx, titleIdx, discName, s.cfg.OutputDir)
		// Ignore submit errors (e.g. duplicate active drive) and continue.
		_ = s.ripEngine.Submit(job)
	}

	return c.Redirect(http.StatusSeeOther, "/queue")
}

// handleDriveRescan clears any existing mapping for a drive and redirects back to the detail page.
// TODO: delete disc mapping from store when the mapping layer is implemented.
func (s *Server) handleDriveRescan(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	return c.Redirect(http.StatusSeeOther, "/drives/"+strconv.Itoa(idx))
}
