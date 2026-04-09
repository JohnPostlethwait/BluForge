package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/db"
)

// ---------------------------------------------------------------------------
// GET /contributions (list page)
// ---------------------------------------------------------------------------

func TestHandleContributions_Empty(t *testing.T) {
	srv, _ := setupContribServer(t)

	req := httptest.NewRequest(http.MethodGet, "/contributions", nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)

	err := srv.handleContributions(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "No contributions yet") {
		t.Errorf("body missing 'No contributions yet', got:\n%s", body)
	}
}

func TestHandleContributions_WithEntries(t *testing.T) {
	srv, store := setupContribServer(t)

	// Seed two contributions with distinct disc names.
	c1 := db.Contribution{
		DiscKey:   "disc-key-alpha",
		DiscName:  "ALPHA_DISC",
		RawOutput: "TINFO:0,2,0,\"Alpha\"",
		ScanJSON:  `{}`,
	}
	if _, err := store.SaveContribution(c1); err != nil {
		t.Fatalf("SaveContribution c1: %v", err)
	}

	c2 := db.Contribution{
		DiscKey:   "disc-key-bravo",
		DiscName:  "BRAVO_DISC",
		RawOutput: "TINFO:0,2,0,\"Bravo\"",
		ScanJSON:  `{}`,
	}
	if _, err := store.SaveContribution(c2); err != nil {
		t.Fatalf("SaveContribution c2: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/contributions", nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)

	err := srv.handleContributions(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ALPHA_DISC") {
		t.Errorf("body missing 'ALPHA_DISC'")
	}
	if !strings.Contains(body, "BRAVO_DISC") {
		t.Errorf("body missing 'BRAVO_DISC'")
	}
}

func TestHandleContributions_NoPATShowsBanner(t *testing.T) {
	srv, store := setupContribServer(t)

	// Seed a pending contribution so the table renders.
	_ = seedTestContribution(t, store)

	// cfg.GitHubToken is empty by default (setupContribServer uses &config.AppConfig{}).
	req := httptest.NewRequest(http.MethodGet, "/contributions", nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)

	if err := srv.handleContributions(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "GitHub token is not configured") {
		t.Errorf("expected PAT warning banner, got body:\n%s", body)
	}
	// Contribute button must be a disabled <button>, not an <a>.
	if strings.Contains(body, `href="/contributions/`) {
		t.Errorf("expected no 'Contribute' link when PAT missing, but found href")
	}
	if !strings.Contains(body, "disabled") {
		t.Errorf("expected disabled attribute on Contribute button when PAT missing")
	}
}

func TestHandleContributions_WithPATShowsLink(t *testing.T) {
	srv, store := setupContribServer(t)
	srv.cfg.GitHubToken = "ghp_test_token"

	_ = seedTestContribution(t, store)

	req := httptest.NewRequest(http.MethodGet, "/contributions", nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)

	if err := srv.handleContributions(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `href="/contributions/`) {
		t.Errorf("expected Contribute link when PAT configured")
	}
	if strings.Contains(body, "GitHub token is not configured") {
		t.Errorf("expected no warning banner when PAT configured")
	}
}

func TestHandleContributions_FlashParam(t *testing.T) {
	srv, _ := setupContribServer(t)

	req := httptest.NewRequest(http.MethodGet, "/contributions?flash=Contribution+submitted", nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)

	if err := srv.handleContributions(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Contribution submitted") {
		t.Errorf("expected flash message in body, got:\n%s", body)
	}
	if !strings.Contains(body, "alert-success") {
		t.Errorf("expected alert-success class in body")
	}
}

func TestHandleContributions_FlashParamTruncated(t *testing.T) {
	srv, _ := setupContribServer(t)

	longFlash := strings.Repeat("x", 300)
	req := httptest.NewRequest(http.MethodGet, "/contributions?flash="+longFlash, nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)

	if err := srv.handleContributions(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if strings.Contains(body, longFlash) {
		t.Errorf("expected long flash to be truncated, but found full string in body")
	}
}
