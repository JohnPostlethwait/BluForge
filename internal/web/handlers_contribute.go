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
	"github.com/johnpostlethwait/bluforge/templates"
)

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
	}).Render(c.Request().Context(), c.Response().Writer)
}

// handleContributionDetail renders the contribution detail/editing form.
func (s *Server) handleContributionDetail(c echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid contribution id")
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
	}).Render(c.Request().Context(), c.Response().Writer)
}

// handleContributionSave saves a draft contribution from form values.
func (s *Server) handleContributionSave(c echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid contribution id")
	}

	tmdbID := c.FormValue("tmdb_id")
	upc := c.FormValue("upc")
	regionCode := c.FormValue("region_code")
	format := c.FormValue("format")
	titleLabelsRaw := c.FormValue("title_labels")

	year := 0
	if v := c.FormValue("year"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			year = n
		}
	}

	ri := contribute.ReleaseInfo{
		UPC:        upc,
		RegionCode: regionCode,
		Year:       year,
		Format:     format,
		Slug:       contribute.ReleaseSlug(year, format),
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

	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/contributions/%d", id))
}

// handleContributionSubmit submits a contribution to GitHub as a PR.
func (s *Server) handleContributionSubmit(c echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid contribution id")
	}

	cfg := s.GetConfig()
	if cfg.GitHubToken == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "GitHub token is not configured. Please set it in Settings.")
	}

	// Save the current form state before submitting so we don't lose user input.
	tmdbID := c.FormValue("tmdb_id")
	upc := c.FormValue("upc")
	regionCode := c.FormValue("region_code")
	format := c.FormValue("format")
	titleLabelsRaw := c.FormValue("title_labels")

	year := 0
	if v := c.FormValue("year"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			year = n
		}
	}

	ri := contribute.ReleaseInfo{
		UPC:        upc,
		RegionCode: regionCode,
		Year:       year,
		Format:     format,
		Slug:       contribute.ReleaseSlug(year, format),
	}

	riBytes, err := json.Marshal(ri)
	if err != nil {
		slog.Error("failed to marshal release info", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save draft before submit.")
	}

	if err := s.store.UpdateContributionDraft(id, tmdbID, string(riBytes), titleLabelsRaw); err != nil {
		slog.Error("failed to save draft before submit", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save draft before submit.")
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

	svc := contribute.NewService(s.store, ghClient)
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

	return c.Redirect(http.StatusSeeOther, "/contributions")
}

// handleContributionDelete removes a pending/drafting contribution.
func (s *Server) handleContributionDelete(c echo.Context) error {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid contribution id")
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
