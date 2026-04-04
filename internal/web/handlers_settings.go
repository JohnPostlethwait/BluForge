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
		OutputDir:             cfg.OutputDir,
		AutoRip:               cfg.AutoRip,
		MinTitleLength:        strconv.Itoa(cfg.MinTitleLength),
		PollInterval:          strconv.Itoa(cfg.PollInterval),
		DuplicateAction:       cfg.DuplicateAction,
		GitHubClientID:        cfg.GitHubClientID,
		GitHubClientSecret:    cfg.GitHubClientSecret,
		GitHubToken:           cfg.GitHubToken,
		MakeMKVKey:            cfg.MakeMKVKey,
		PreferredAudioLangs:   cfg.PreferredAudioLangs,
		PreferredSubtitleLangs: cfg.PreferredSubtitleLangs,
		KeepForcedSubtitles:   cfg.KeepForcedSubtitles,
		KeepLosslessAudio:     cfg.KeepLosslessAudio,
		CSRFToken:             csrfToken(c),
	}
	return templates.Settings(data).Render(c.Request().Context(), c.Response().Writer)
}

func (s *Server) handleSettingsSave(c echo.Context) error {
	outputDir := c.FormValue("output_dir")
	autoRip := c.FormValue("auto_rip") == "true"
	duplicateAction := c.FormValue("duplicate_action")
	githubClientID := c.FormValue("github_client_id")
	githubClientSecret := c.FormValue("github_client_secret")
	githubToken := c.FormValue("github_token")
	makemkvKey := c.FormValue("makemkv_key")
	preferredAudioLangs := c.FormValue("preferred_audio_langs")
	preferredSubtitleLangs := c.FormValue("preferred_subtitle_langs")
	keepForcedSubtitles := c.FormValue("keep_forced_subtitles") == "true"
	keepLosslessAudio := c.FormValue("keep_lossless_audio") == "true"

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
		cfg.GitHubClientID = githubClientID
		cfg.PreferredAudioLangs = preferredAudioLangs
		cfg.PreferredSubtitleLangs = preferredSubtitleLangs
		cfg.KeepForcedSubtitles = keepForcedSubtitles
		cfg.KeepLosslessAudio = keepLosslessAudio

		// Only update the secret if the user provided a non-masked value.
		if githubClientSecret != "" && githubClientSecret != "••••••••" {
			cfg.GitHubClientSecret = githubClientSecret
		}

		// Only update the token if the user provided a non-masked value.
		if githubToken != "" && githubToken != "••••••••" {
			cfg.GitHubToken = githubToken
		}

		// Only update the key if the user provided a non-masked value.
		// Unlike the GitHub secret guard, empty string IS allowed here — it
		// clears the key, reverting MakeMKV to trial mode (intentional).
		if makemkvKey != "••••••••" {
			cfg.MakeMKVKey = makemkvKey
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

	if makemkvKey != "••••••••" && s.onMakeMKVKeyChange != nil {
		s.onMakeMKVKeyChange(s.GetConfig().MakeMKVKey)
	}

	return c.Redirect(http.StatusSeeOther, "/settings")
}
