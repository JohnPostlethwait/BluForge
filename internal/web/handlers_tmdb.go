package web

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/tmdb"
)

// handleTMDBSearch proxies a TMDB title search and returns JSON results.
// Called by Alpine fetch() on the contribution detail page.
// Route: GET /api/tmdb/search?q=<query>&type=<movie|series>
//
// CSRF is not required: the existing skipper bypasses validation for
// Accept: application/json requests.
func (s *Server) handleTMDBSearch(c echo.Context) error {
	cfg := s.GetConfig()
	if cfg.TMDBApiKey == "" {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "TMDB API key is not configured.")
	}

	q := c.QueryParam("q")
	if q == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "q parameter is required.")
	}
	mediaType := c.QueryParam("type")
	if mediaType == "" {
		mediaType = tmdb.MediaTypeMovie
	}

	opts := []tmdb.Option{}
	if s.tmdbBaseURL != "" {
		opts = append(opts, tmdb.WithBaseURL(s.tmdbBaseURL))
	}
	client := tmdb.NewClient(cfg.TMDBApiKey, opts...)

	results, err := client.Search(c.Request().Context(), q, mediaType)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, "TMDB search failed: "+err.Error())
	}

	return c.JSON(http.StatusOK, results)
}
