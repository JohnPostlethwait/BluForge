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

// parseAndSaveDraft reads the contribution form fields from c, builds the
// ReleaseInfo JSON blob, and persists a draft via the store. Returns an HTTP
// error on any failure.
func (s *Server) parseAndSaveDraft(c echo.Context, id int64) error {
	tmdbID := c.FormValue("tmdb_id")
	upc := c.FormValue("upc")
	regionCode := c.FormValue("region_code")
	format := c.FormValue("format")
	mediaType := c.FormValue("media_type")
	titleLabelsRaw := c.FormValue("title_labels")

	year := 0
	if v := c.FormValue("year"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			year = n
		}
	}

	asin := c.FormValue("asin")
	releaseDate := c.FormValue("release_date")
	frontImageURL := c.FormValue("front_image_url")

	ri := contribute.ReleaseInfo{
		UPC:           upc,
		RegionCode:    regionCode,
		Year:          year,
		Format:        format,
		Slug:          contribute.ReleaseSlug(year, format),
		MediaType:     mediaType,
		ASIN:          asin,
		ReleaseDate:   releaseDate,
		FrontImageURL: frontImageURL,
	}

	riBytes, err := json.Marshal(ri)
	if err != nil {
		slog.Error("failed to marshal release info", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save draft.")
	}

	if err := s.store.UpdateContributionDraft(id, tmdbID, string(riBytes), titleLabelsRaw); err != nil {
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

	if err := s.parseAndSaveDraft(c, id); err != nil {
		return err
	}

	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/contributions/%d", id))
}

// handleContributionSubmit submits a contribution to GitHub as a PR.
func (s *Server) handleContributionSubmit(c echo.Context) error {
	id, err := parseContribID(c)
	if err != nil {
		return err
	}

	cfg := s.GetConfig()
	if cfg.GitHubToken == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "GitHub token is not configured. Please set it in Settings.")
	}

	// Save the current form state before submitting so we don't lose user input.
	if err := s.parseAndSaveDraft(c, id); err != nil {
		return err
	}

	// Reject if all titles are omitted (empty type).
	titleLabelsRaw := c.FormValue("title_labels")
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

	// Now extract media metadata for the submit call.
	mediaTitle := c.FormValue("media_title")
	mediaType := c.FormValue("media_type")
	mediaYear := 0
	if v := c.FormValue("media_year"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			mediaYear = n
		}
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
	prURL, err := svc.Submit(c.Request().Context(), id, mediaTitle, mediaYear, mediaType)
	if err != nil {
		slog.Error("failed to submit contribution", "id", id, "error", err)
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

// handleContributionResubmit pushes a corrective commit to the existing PR branch.
// Used when the PR was opened with a generation bug and the files need to be
// regenerated and re-pushed without opening a new PR.
func (s *Server) handleContributionResubmit(c echo.Context) error {
	id, err := parseContribID(c)
	if err != nil {
		return err
	}

	cfg := s.GetConfig()
	if cfg.GitHubToken == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "GitHub token is not configured. Please set it in Settings.")
	}

	mediaTitle := c.FormValue("media_title")
	mediaType := c.FormValue("media_type")
	mediaYear := 0
	if v := c.FormValue("media_year"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			mediaYear = n
		}
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
	if err := svc.Resubmit(c.Request().Context(), id, mediaTitle, mediaYear, mediaType); err != nil {
		slog.Error("failed to resubmit contribution", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to resubmit contribution: "+err.Error())
	}

	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/contributions/%d?flash=PR+updated+%%E2%%80%%94+corrected+files+pushed+to+branch", id))
}

// parseAndSaveUpdateDraft saves title_labels for an update contribution.
// Unlike parseAndSaveDraft, it preserves the existing tmdb_id and release_info.
func (s *Server) parseAndSaveUpdateDraft(c echo.Context, id int64) error {
	contrib, err := s.store.GetContribution(id)
	if err != nil || contrib == nil {
		slog.Error("failed to load contribution for update draft", "id", id)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load contribution.")
	}
	titleLabelsRaw := c.FormValue("title_labels")
	if err := s.store.UpdateContributionDraft(id, contrib.TmdbID, contrib.ReleaseInfo, titleLabelsRaw); err != nil {
		slog.Error("failed to update contribution draft", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save draft.")
	}
	return nil
}

// handleContributionSubmitUpdate submits an update contribution to GitHub as a PR.
func (s *Server) handleContributionSubmitUpdate(c echo.Context) error {
	id, err := parseContribID(c)
	if err != nil {
		return err
	}

	cfg := s.GetConfig()
	if cfg.GitHubToken == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "GitHub token is not configured. Please set it in Settings.")
	}

	if err := s.parseAndSaveUpdateDraft(c, id); err != nil {
		return err
	}

	// Reject if all titles are omitted.
	titleLabelsRaw := c.FormValue("title_labels")
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
	prURL, err := svc.SubmitUpdate(c.Request().Context(), id)
	if err != nil {
		slog.Error("failed to submit update contribution", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to submit update: "+err.Error())
	}

	sseData, _ := json.Marshal(map[string]any{"id": id, "prURL": prURL})
	s.sseHub.Broadcast(SSEEvent{Event: "contribution_submitted", Data: string(sseData)})

	return c.Redirect(http.StatusSeeOther, "/contributions?flash=Update+submitted+%E2%80%94+PR+opened+successfully")
}

// handleContributionResubmitUpdate pushes corrective files to the existing update PR branch.
func (s *Server) handleContributionResubmitUpdate(c echo.Context) error {
	id, err := parseContribID(c)
	if err != nil {
		return err
	}

	cfg := s.GetConfig()
	if cfg.GitHubToken == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "GitHub token is not configured. Please set it in Settings.")
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
	if err := svc.ResubmitUpdate(c.Request().Context(), id); err != nil {
		slog.Error("failed to resubmit update contribution", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to resubmit update: "+err.Error())
	}

	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/contributions/%d?flash=PR+updated+%%E2%%80%%94+corrected+files+pushed+to+branch", id))
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
