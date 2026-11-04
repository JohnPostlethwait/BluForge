package web

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/templates"
)

func (s *Server) handleSettings(c echo.Context) error {
	cfg := s.GetConfig()
	data := templates.SettingsData{
		OutputDir:              cfg.OutputDir,
		AutoRip:                cfg.AutoRip,
		MinTitleLength:         strconv.Itoa(cfg.MinTitleLength),
		PollInterval:           strconv.Itoa(cfg.PollInterval),
		DuplicateAction:        cfg.DuplicateAction,
		GitHubToken:            cfg.GitHubToken,
		MakeMKVKey:             cfg.MakeMKVKey,
		TMDBApiKey:             cfg.TMDBApiKey,
		PreferredAudioLangs:    cfg.PreferredAudioLangs,
		PreferredSubtitleLangs: cfg.PreferredSubtitleLangs,
		KeepForcedSubtitles:    cfg.KeepForcedSubtitles,
		KeepLosslessAudio:      cfg.KeepLosslessAudio,
		CSRFToken:              csrfToken(c),
		KeyDBStatus:            c.QueryParam("keydb"),
		KeyDBKeys:              c.QueryParam("keys"),
	}
	var listed []makemkv.DriveInfo
	if s.driveMgr != nil {
		listed = s.driveMgr.ListedDrives()
	}
	data.Drives = ignoredDriveOptions(listed, cfg.IgnoredDrives)
	return templates.Settings(data).Render(c.Request().Context(), c.Response().Writer)
}

func (s *Server) handleSettingsSave(c echo.Context) error {
	outputDir := c.FormValue("output_dir")
	autoRip := c.FormValue("auto_rip") == "true"
	duplicateAction := c.FormValue("duplicate_action")
	githubToken := c.FormValue("github_token")
	makemkvKey := c.FormValue("makemkv_key")
	tmdbApiKey := c.FormValue("tmdb_api_key")
	preferredAudioLangs := c.FormValue("preferred_audio_langs")
	preferredSubtitleLangs := c.FormValue("preferred_subtitle_langs")
	keepForcedSubtitles := c.FormValue("keep_forced_subtitles") == "true"
	keepLosslessAudio := c.FormValue("keep_lossless_audio") == "true"

	form, err := c.FormParams()
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Could not read the form.")
	}
	ignoredDrives := uniqueDrives(form["ignored_drives"])

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
		cfg.PreferredAudioLangs = preferredAudioLangs
		cfg.PreferredSubtitleLangs = preferredSubtitleLangs
		cfg.KeepForcedSubtitles = keepForcedSubtitles
		cfg.KeepLosslessAudio = keepLosslessAudio
		cfg.IgnoredDrives = ignoredDrives

		// Only update the token if the user provided a non-masked value.
		if githubToken != "" && githubToken != "••••••••" {
			cfg.GitHubToken = githubToken
		}

		// Only update the TMDB key if the user provided a non-masked value.
		if tmdbApiKey != "" && tmdbApiKey != "••••••••" {
			cfg.TMDBApiKey = tmdbApiKey
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

// ignoredDriveOptions lists the drives for the "Ignored drives" setting: every
// drive attached now, in listing order, then every ignored drive that is not
// attached, so it can be un-ignored. Drives are matched by
// makemkv.DriveIdentity, so a drive whose stored name has an older firmware
// revision is listed once, under the name it reports today. A drive listed
// without a name cannot be identified, so it cannot be ignored and is left out.
func ignoredDriveOptions(listed []makemkv.DriveInfo, ignored []string) []templates.IgnoredDriveOption {
	ignoredIDs := make(map[string]bool, len(ignored))
	for _, name := range ignored {
		ignoredIDs[makemkv.DriveIdentity(name)] = true
	}

	var out []templates.IgnoredDriveOption
	shown := make(map[string]bool)
	for _, d := range listed {
		id := d.Identity()
		if id == "" || shown[id] {
			continue
		}
		shown[id] = true
		out = append(out, templates.IgnoredDriveOption{
			Name:       d.DriveName,
			DevicePath: d.DevicePath,
			Ignored:    ignoredIDs[id],
		})
	}
	for _, name := range ignored {
		id := makemkv.DriveIdentity(name)
		if id == "" || shown[id] {
			continue
		}
		shown[id] = true
		out = append(out, templates.IgnoredDriveOption{Name: name, Ignored: true})
	}
	return out
}

// uniqueDrives drops blank names and any name that is the same drive as one
// before it.
func uniqueDrives(names []string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, name := range names {
		id := makemkv.DriveIdentity(name)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, name)
	}
	return out
}
