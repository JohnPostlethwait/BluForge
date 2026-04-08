package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/contribute"
	"github.com/johnpostlethwait/bluforge/internal/db"
	ghpkg "github.com/johnpostlethwait/bluforge/internal/github"
	"github.com/johnpostlethwait/bluforge/internal/tmdb"
	"github.com/johnpostlethwait/bluforge/templates"
)

// parseContribID extracts and validates the ":id" route parameter as an int64.
func parseContribID(c echo.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return 0, echo.NewHTTPError(http.StatusBadRequest, "invalid contribution id")
	}
	return id, nil
}

// saveDraft reads user-editable form fields and merges them into the contribution's
// existing release_info, preserving non-editable fields. Always persists title_labels.
func (s *Server) saveDraft(c echo.Context, id int64) error {
	contrib, err := s.store.GetContribution(id)
	if err != nil || contrib == nil {
		slog.Error("failed to load contribution for draft save", "id", id)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load contribution.")
	}

	// Parse existing release_info so we can merge without overwriting.
	var ri contribute.ReleaseInfo
	if contrib.ReleaseInfo != "" {
		if err := json.Unmarshal([]byte(contrib.ReleaseInfo), &ri); err != nil {
			slog.Error("failed to parse release_info for draft save", "id", id, "error", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load contribution.")
		}
	}

	// Merge user-editable fields from form (only overwrite if provided).
	if v := c.FormValue("asin"); v != "" {
		ri.ASIN = v
	}
	if v := c.FormValue("release_date"); v != "" {
		ri.ReleaseDate = v
	}
	if v := c.FormValue("front_image_url"); v != "" {
		ri.FrontImageURL = v
	}

	// Add-specific fields (only present on add draft forms).
	if v := c.FormValue("upc"); v != "" {
		ri.UPC = v
	}
	if v := c.FormValue("region_code"); v != "" {
		ri.RegionCode = v
	}
	if v := c.FormValue("format"); v != "" {
		ri.Format = v
		ri.Slug = contribute.ReleaseSlug(ri.Year, ri.Format)
	}
	if v := c.FormValue("media_type"); v != "" {
		ri.MediaType = v
	}
	if v := c.FormValue("year"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ri.Year = n
			if ri.Format != "" {
				ri.Slug = contribute.ReleaseSlug(ri.Year, ri.Format)
			}
		}
	}

	riBytes, err := json.Marshal(ri)
	if err != nil {
		slog.Error("failed to marshal release_info", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save draft.")
	}

	// tmdb_id: use form value if present, otherwise preserve existing.
	tmdbID := c.FormValue("tmdb_id")
	if tmdbID == "" {
		tmdbID = contrib.TmdbID
	}

	// title_labels: use form value if present, otherwise preserve existing.
	titleLabels := c.FormValue("title_labels")
	if titleLabels == "" {
		titleLabels = contrib.TitleLabels
	}

	if err := s.store.UpdateContributionDraft(id, tmdbID, string(riBytes), titleLabels); err != nil {
		slog.Error("failed to update contribution draft", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save draft.")
	}

	return nil
}

// handleContributions renders the contributions queue page.
func (s *Server) handleContributions(c echo.Context) error {
	contributions, err := s.store.ListContributions("")
	if err != nil {
		slog.Error("failed to list contributions", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load contributions.")
	}

	if contributions == nil {
		contributions = []db.Contribution{}
	}

	cfg := s.GetConfig()
	return templates.Contributions(templates.ContributionsData{
		Contributions:         contributions,
		GitHubTokenConfigured: cfg.GitHubToken != "",
		Flash:                 truncateFlash(c),
	}).Render(c.Request().Context(), c.Response().Writer)
}

// handleContributionDetail renders the contribution detail/editing form.
func (s *Server) handleContributionDetail(c echo.Context) error {
	id, err := parseContribID(c)
	if err != nil {
		return err
	}

	contrib, err := s.store.GetContribution(id)
	if err != nil {
		slog.Error("failed to get contribution", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load contribution.")
	}
	if contrib == nil {
		return echo.NewHTTPError(http.StatusNotFound, "Contribution not found.")
	}

	cfg := s.GetConfig()
	return templates.ContributionDetail(templates.ContributionDetailData{
		Contribution:          *contrib,
		CSRFToken:             csrfToken(c),
		GitHubTokenConfigured: cfg.GitHubToken != "",
		TMDBConfigured:        cfg.TMDBApiKey != "",
		Flash:                 truncateFlash(c),
	}).Render(c.Request().Context(), c.Response().Writer)
}

// handleContributionSave saves a draft contribution from form values.
func (s *Server) handleContributionSave(c echo.Context) error {
	id, err := parseContribID(c)
	if err != nil {
		return err
	}

	if err := s.saveDraft(c, id); err != nil {
		return err
	}

	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/contributions/%d", id))
}

// handleContributionSubmit handles submit and resubmit for both add and update contributions.
func (s *Server) handleContributionSubmit(c echo.Context) error {
	id, err := parseContribID(c)
	if err != nil {
		return err
	}

	cfg := s.GetConfig()
	if cfg.GitHubToken == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "GitHub token is not configured. Please set it in Settings.")
	}

	// Persist the current form state before submitting.
	if err := s.saveDraft(c, id); err != nil {
		return err
	}

	// Validate title labels.
	titleLabelsRaw := c.FormValue("title_labels")
	if titleLabelsRaw == "" {
		// If form didn't carry title_labels, load from DB (already persisted by saveDraft).
		contrib, err := s.store.GetContribution(id)
		if err != nil || contrib == nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load contribution.")
		}
		titleLabelsRaw = contrib.TitleLabels
	}
	var titleLabels []contribute.TitleLabel
	if titleLabelsRaw != "" {
		if err := json.Unmarshal([]byte(titleLabelsRaw), &titleLabels); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid title labels.")
		}
	}
	hasTyped := false
	for _, l := range titleLabels {
		if l.Type != "" {
			hasTyped = true
			break
		}
	}
	if !hasTyped {
		return echo.NewHTTPError(http.StatusBadRequest, "At least one title must have a type assigned before submitting.")
	}

	ghClient, err := ghpkg.NewClient(cfg.GitHubToken)
	if err != nil {
		slog.Error("failed to create GitHub client", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create GitHub client.")
	}

	tmdbOpts := []tmdb.Option{}
	if s.tmdbBaseURL != "" {
		tmdbOpts = append(tmdbOpts, tmdb.WithBaseURL(s.tmdbBaseURL))
	}
	tmdbClient := tmdb.NewClient(cfg.TMDBApiKey, tmdbOpts...)
	svc := contribute.NewService(s.store, ghClient, tmdbClient)
	prURL, err := svc.Execute(c.Request().Context(), id)
	if err != nil {
		slog.Error("failed to execute contribution", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to submit contribution: "+err.Error())
	}

	sseData, _ := json.Marshal(map[string]any{
		"id":    id,
		"prURL": prURL,
	})
	s.sseHub.Broadcast(SSEEvent{
		Event: "contribution_submitted",
		Data:  string(sseData),
	})

	return c.Redirect(http.StatusSeeOther, "/contributions?flash=Contribution+submitted+%E2%80%94+PR+opened+successfully")
}

// handleContributionDelete removes a pending/drafting contribution.
func (s *Server) handleContributionDelete(c echo.Context) error {
	id, err := parseContribID(c)
	if err != nil {
		return err
	}

	contrib, err := s.store.GetContribution(id)
	if err != nil {
		slog.Error("failed to get contribution for delete", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load contribution.")
	}
	if contrib == nil {
		return echo.NewHTTPError(http.StatusNotFound, "Contribution not found.")
	}
	if contrib.Status == "submitted" {
		return echo.NewHTTPError(http.StatusBadRequest, "Cannot delete a submitted contribution.")
	}

	if err := s.store.DeleteContribution(id); err != nil {
		slog.Error("failed to delete contribution", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to delete contribution.")
	}

	return c.Redirect(http.StatusSeeOther, "/contributions")
}
