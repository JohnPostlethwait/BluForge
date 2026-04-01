package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
)

// testSettingsServer creates a Server with a minimal config and a real config
// file on disk so that UpdateConfig can persist changes.
func testSettingsServer(t *testing.T) *Server {
	t.Helper()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")

	cfg := &config.AppConfig{
		OutputDir:          "/old/output",
		AutoRip:            false,
		MinTitleLength:     120,
		PollInterval:       5,
		DuplicateAction:    "skip",
		GitHubClientID:     "",
		GitHubClientSecret: "real-secret",
	}

	if err := config.Save(*cfg, cfgPath); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	e := echo.New()
	s := &Server{
		echo:          e,
		cfg:           cfg,
		configPath:    cfgPath,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	e.POST("/settings", s.handleSettingsSave)

	return s
}

func TestHandleSettingsSave_UpdatesConfig(t *testing.T) {
	srv := testSettingsServer(t)

	form := url.Values{}
	form.Set("output_dir", "/new/output")
	form.Set("auto_rip", "true")
	form.Set("min_title_length", "90")
	form.Set("poll_interval", "10")
	form.Set("duplicate_action", "overwrite")
	form.Set("github_client_id", "new-id")
	form.Set("github_client_secret", "new-secret")

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", rec.Code, rec.Body.String())
	}

	cfg := srv.GetConfig()

	if cfg.OutputDir != "/new/output" {
		t.Errorf("OutputDir: expected %q, got %q", "/new/output", cfg.OutputDir)
	}
	if !cfg.AutoRip {
		t.Error("AutoRip: expected true, got false")
	}
	if cfg.MinTitleLength != 90 {
		t.Errorf("MinTitleLength: expected 90, got %d", cfg.MinTitleLength)
	}
	if cfg.PollInterval != 10 {
		t.Errorf("PollInterval: expected 10, got %d", cfg.PollInterval)
	}
	if cfg.DuplicateAction != "overwrite" {
		t.Errorf("DuplicateAction: expected %q, got %q", "overwrite", cfg.DuplicateAction)
	}
	if cfg.GitHubClientID != "new-id" {
		t.Errorf("GitHubClientID: expected %q, got %q", "new-id", cfg.GitHubClientID)
	}
	if cfg.GitHubClientSecret != "new-secret" {
		t.Errorf("GitHubClientSecret: expected %q, got %q", "new-secret", cfg.GitHubClientSecret)
	}
}

func TestHandleSettingsSave_MaskedSecret(t *testing.T) {
	srv := testSettingsServer(t)

	form := url.Values{}
	form.Set("output_dir", "/new/output")
	form.Set("auto_rip", "true")
	form.Set("min_title_length", "90")
	form.Set("poll_interval", "10")
	form.Set("duplicate_action", "overwrite")
	form.Set("github_client_id", "new-id")
	form.Set("github_client_secret", "••••••••")

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", rec.Code, rec.Body.String())
	}

	cfg := srv.GetConfig()

	if cfg.GitHubClientSecret != "real-secret" {
		t.Errorf("GitHubClientSecret should remain %q when masked value is submitted, got %q",
			"real-secret", cfg.GitHubClientSecret)
	}
}

func TestHandleSettingsSave_PartialUpdate(t *testing.T) {
	srv := testSettingsServer(t)

	// Only set output_dir; leave min_title_length and poll_interval empty so
	// the handler parses them as -1 and skips updating those fields.
	form := url.Values{}
	form.Set("output_dir", "/new/path")

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d: %s", rec.Code, rec.Body.String())
	}

	cfg := srv.GetConfig()

	if cfg.OutputDir != "/new/path" {
		t.Errorf("OutputDir: expected %q, got %q", "/new/path", cfg.OutputDir)
	}

	// MinTitleLength and PollInterval should be unchanged because empty form
	// values result in -1 which the handler skips.
	if cfg.MinTitleLength != 120 {
		t.Errorf("MinTitleLength should remain 120 when form value is empty, got %d", cfg.MinTitleLength)
	}
	if cfg.PollInterval != 5 {
		t.Errorf("PollInterval should remain 5 when form value is empty, got %d", cfg.PollInterval)
	}

	// These fields are always set from form values, so they become zero/empty
	// when omitted from the form.
	if cfg.AutoRip {
		t.Error("AutoRip: expected false when omitted from form")
	}
	if cfg.DuplicateAction != "" {
		t.Errorf("DuplicateAction: expected empty string when omitted, got %q", cfg.DuplicateAction)
	}
	if cfg.GitHubClientID != "" {
		t.Errorf("GitHubClientID: expected empty string when omitted, got %q", cfg.GitHubClientID)
	}
}
