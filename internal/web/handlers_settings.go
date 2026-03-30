package web

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/templates"
)

func (s *Server) handleSettings(c echo.Context) error {
	cfg := s.GetConfig()
	data := templates.SettingsData{
		OutputDir:          cfg.OutputDir,
		AutoRip:            cfg.AutoRip,
		MinTitleLength:     strconv.Itoa(cfg.MinTitleLength),
		PollInterval:       strconv.Itoa(cfg.PollInterval),
		DuplicateAction:    cfg.DuplicateAction,
		MovieTemplate:      cfg.MovieTemplate,
		SeriesTemplate:     cfg.SeriesTemplate,
		GitHubClientID:     cfg.GitHubClientID,
		GitHubClientSecret: cfg.GitHubClientSecret,
		CSRFToken:          csrfToken(c),
	}
	return templates.Settings(data).Render(c.Request().Context(), c.Response().Writer)
}

func (s *Server) handleSettingsSave(c echo.Context) error {
	outputDir := c.FormValue("output_dir")
	autoRip := c.FormValue("auto_rip") == "true"
	duplicateAction := c.FormValue("duplicate_action")
	movieTemplate := c.FormValue("movie_template")
	seriesTemplate := c.FormValue("series_template")
	githubClientID := c.FormValue("github_client_id")
	githubClientSecret := c.FormValue("github_client_secret")

	minTitleLength := -1
	if v := c.FormValue("min_title_length"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			minTitleLength = n
		}
	}
	pollInterval := -1
	if v := c.FormValue("poll_interval"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			pollInterval = n
		}
	}

	if err := s.UpdateConfig(func(cfg *config.AppConfig) {
		cfg.OutputDir = outputDir
		cfg.AutoRip = autoRip
		cfg.DuplicateAction = duplicateAction
		cfg.MovieTemplate = movieTemplate
		cfg.SeriesTemplate = seriesTemplate
		cfg.GitHubClientID = githubClientID

		// Only update the secret if the user provided a non-masked value.
		if githubClientSecret != "" && githubClientSecret != "••••••••" {
			cfg.GitHubClientSecret = githubClientSecret
		}

		if minTitleLength >= 0 {
			cfg.MinTitleLength = minTitleLength
		}
		if pollInterval >= 0 {
			cfg.PollInterval = pollInterval
		}
	}); err != nil {
		slog.Error("failed to save settings", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save settings.")
	}

	return c.Redirect(http.StatusSeeOther, "/settings")
}
