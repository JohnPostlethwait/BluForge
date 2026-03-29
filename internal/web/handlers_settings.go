package web

import (
	"net/http"
	"strconv"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/templates"
	"github.com/labstack/echo/v4"
)

func (s *Server) handleSettings(c echo.Context) error {
	data := templates.SettingsData{
		OutputDir:          s.cfg.OutputDir,
		AutoRip:            s.cfg.AutoRip,
		MinTitleLength:     strconv.Itoa(s.cfg.MinTitleLength),
		PollInterval:       strconv.Itoa(s.cfg.PollInterval),
		DuplicateAction:    s.cfg.DuplicateAction,
		MovieTemplate:      s.cfg.MovieTemplate,
		SeriesTemplate:     s.cfg.SeriesTemplate,
		GitHubClientID:     s.cfg.GitHubClientID,
		GitHubClientSecret: s.cfg.GitHubClientSecret,
	}
	return templates.Settings(data).Render(c.Request().Context(), c.Response().Writer)
}

func (s *Server) handleSettingsSave(c echo.Context) error {
	s.cfg.OutputDir = c.FormValue("output_dir")
	s.cfg.AutoRip = c.FormValue("auto_rip") == "true"
	s.cfg.DuplicateAction = c.FormValue("duplicate_action")
	s.cfg.MovieTemplate = c.FormValue("movie_template")
	s.cfg.SeriesTemplate = c.FormValue("series_template")
	s.cfg.GitHubClientID = c.FormValue("github_client_id")

	// Only update the secret if the user provided a non-masked value.
	if secret := c.FormValue("github_client_secret"); secret != "" && secret != "••••••••" {
		s.cfg.GitHubClientSecret = secret
	}

	if v := c.FormValue("min_title_length"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			s.cfg.MinTitleLength = n
		}
	}
	if v := c.FormValue("poll_interval"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			s.cfg.PollInterval = n
		}
	}

	if err := config.Save(*s.cfg, "/config/config.yaml"); err != nil {
		return err
	}

	return c.Redirect(http.StatusSeeOther, "/settings")
}
