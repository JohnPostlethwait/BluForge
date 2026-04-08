package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/db"
)

// ---------------------------------------------------------------------------
// Setup helpers
// ---------------------------------------------------------------------------

func setupContribServer(t *testing.T) (*Server, *db.Store) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cfg := &config.AppConfig{OutputDir: tmpDir}

	srv := &Server{
		echo:          echo.New(),
		cfg:           cfg,
		store:         store,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	return srv, store
}

func seedTestContribution(t *testing.T, store *db.Store) int64 {
	t.Helper()
	c := db.Contribution{
		DiscKey:   "test-disc-key-001",
		DiscName:  "TEST_DISC",
		RawOutput: "TINFO:0,2,0,\"Test Title\"",
		ScanJSON:  `{"DiscName":"TEST_DISC","TitleCount":1,"Titles":[{"Index":0,"Attributes":{"2":"Test Title","9":"1:30:00","10":"25 GB","11":"25000000000","16":"00001.mpls"},"Streams":[]}]}`,
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}
	return id
}

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

func TestHandleContributionDetail_NoPATShowsBanner(t *testing.T) {
	srv, store := setupContribServer(t)
	id := seedTestContribution(t, store)

	// cfg.GitHubToken is empty by default.
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/contributions/%d", id), nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	if err := srv.handleContributionDetail(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "GitHub token is not configured") {
		t.Errorf("expected PAT warning banner, got body:\n%s", body)
	}
	if !strings.Contains(body, `btn-primary" disabled`) {
		t.Errorf("expected server-side disabled attribute on Submit button when PAT missing")
	}
}

// ---------------------------------------------------------------------------
// GET /contributions/:id (detail page)
// ---------------------------------------------------------------------------

func TestHandleContributionDetail_Success(t *testing.T) {
	srv, store := setupContribServer(t)
	id := seedTestContribution(t, store)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/contributions/%d", id), nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	err := srv.handleContributionDetail(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "TEST_DISC") {
		t.Errorf("body missing disc name 'TEST_DISC'")
	}
}

func TestHandleContributionDetail_InvalidID(t *testing.T) {
	srv, _ := setupContribServer(t)

	req := httptest.NewRequest(http.MethodGet, "/contributions/abc", nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("abc")

	err := srv.handleContributionDetail(c)
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("expected *echo.HTTPError, got %T: %v", err, err)
	}
	if he.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", he.Code)
	}
}

func TestHandleContributionDetail_NotFound(t *testing.T) {
	srv, _ := setupContribServer(t)

	req := httptest.NewRequest(http.MethodGet, "/contributions/99999", nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("99999")

	err := srv.handleContributionDetail(c)
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("expected *echo.HTTPError, got %T: %v", err, err)
	}
	if he.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", he.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /contributions/:id (save draft)
// ---------------------------------------------------------------------------

func TestHandleContributionSave_Success(t *testing.T) {
	srv, store := setupContribServer(t)
	id := seedTestContribution(t, store)

	form := url.Values{}
	form.Set("tmdb_id", "tt1234567")
	form.Set("format", "Blu-ray")
	form.Set("year", "2024")
	form.Set("region_code", "A")
	form.Set("upc", "012345678901")
	form.Set("title_labels", `{"0":"Main Feature"}`)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/contributions/%d", id), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	err := srv.handleContributionSave(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	expectedLoc := fmt.Sprintf("/contributions/%d", id)
	if loc != expectedLoc {
		t.Errorf("expected redirect to %q, got %q", expectedLoc, loc)
	}

	// Verify the contribution was updated in the DB.
	contrib, err := store.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	if contrib.TmdbID != "tt1234567" {
		t.Errorf("TmdbID: expected %q, got %q", "tt1234567", contrib.TmdbID)
	}
	if contrib.TitleLabels != `{"0":"Main Feature"}` {
		t.Errorf("TitleLabels: expected %q, got %q", `{"0":"Main Feature"}`, contrib.TitleLabels)
	}

	// Check that release_info JSON contains the expected fields.
	var ri map[string]any
	if err := json.Unmarshal([]byte(contrib.ReleaseInfo), &ri); err != nil {
		t.Fatalf("unmarshal release_info: %v", err)
	}
	if ri["upc"] != "012345678901" {
		t.Errorf("release_info upc: expected %q, got %v", "012345678901", ri["upc"])
	}
	if ri["format"] != "Blu-ray" {
		t.Errorf("release_info format: expected %q, got %v", "Blu-ray", ri["format"])
	}
	if ri["region_code"] != "A" {
		t.Errorf("release_info region_code: expected %q, got %v", "A", ri["region_code"])
	}
	// Year is stored as a JSON number.
	if year, ok := ri["year"].(float64); !ok || int(year) != 2024 {
		t.Errorf("release_info year: expected 2024, got %v", ri["year"])
	}
}

func TestHandleContributionSave_InvalidID(t *testing.T) {
	srv, _ := setupContribServer(t)

	form := url.Values{}
	form.Set("tmdb_id", "tt1234567")

	req := httptest.NewRequest(http.MethodPost, "/contributions/abc", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("abc")

	err := srv.handleContributionSave(c)
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("expected *echo.HTTPError, got %T: %v", err, err)
	}
	if he.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", he.Code)
	}
}

func TestHandleContributionSave_MissingYear(t *testing.T) {
	srv, store := setupContribServer(t)
	id := seedTestContribution(t, store)

	// POST without year field.
	form := url.Values{}
	form.Set("tmdb_id", "tt9999999")
	form.Set("format", "DVD")

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/contributions/%d", id), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	err := srv.handleContributionSave(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", rec.Code)
	}

	// Verify year=0 in release_info.
	contrib, err := store.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	var ri map[string]any
	if err := json.Unmarshal([]byte(contrib.ReleaseInfo), &ri); err != nil {
		t.Fatalf("unmarshal release_info: %v", err)
	}
	if year, ok := ri["year"].(float64); !ok || int(year) != 0 {
		t.Errorf("release_info year: expected 0, got %v", ri["year"])
	}
}

// ---------------------------------------------------------------------------
// POST /contributions/:id/submit
// ---------------------------------------------------------------------------

func TestHandleContributionSubmit_NoGitHubToken(t *testing.T) {
	srv, store := setupContribServer(t)
	id := seedTestContribution(t, store)

	// cfg.GitHubToken is empty by default.
	form := url.Values{}
	form.Set("tmdb_id", "tt1234567")

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/contributions/%d/submit", id), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	err := srv.handleContributionSubmit(c)
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("expected *echo.HTTPError, got %T: %v", err, err)
	}
	if he.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", he.Code)
	}
	msg, _ := he.Message.(string)
	if !strings.Contains(msg, "GitHub token is not configured") {
		t.Errorf("expected message about GitHub token, got %q", msg)
	}
}

func TestHandleContributionSubmit_InvalidID(t *testing.T) {
	srv, _ := setupContribServer(t)

	req := httptest.NewRequest(http.MethodPost, "/contributions/xyz/submit", nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("xyz")

	err := srv.handleContributionSubmit(c)
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("expected *echo.HTTPError, got %T: %v", err, err)
	}
	if he.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", he.Code)
	}
}

// ---------------------------------------------------------------------------
// POST /contributions/:id/delete
// ---------------------------------------------------------------------------

func TestHandleContributionDelete_Success(t *testing.T) {
	srv, store := setupContribServer(t)
	id := seedTestContribution(t, store)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/contributions/%d/delete", id), nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	err := srv.handleContributionDelete(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/contributions") {
		t.Errorf("expected redirect to /contributions, got %q", loc)
	}

	// Verify contribution is gone from DB.
	contrib, err := store.GetContribution(id)
	if err != nil {
		t.Fatalf("GetContribution: %v", err)
	}
	if contrib != nil {
		t.Error("contribution should have been deleted but still exists")
	}
}

func TestHandleContributionDelete_NotFound(t *testing.T) {
	srv, _ := setupContribServer(t)

	req := httptest.NewRequest(http.MethodPost, "/contributions/99999/delete", nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("99999")

	err := srv.handleContributionDelete(c)
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("expected *echo.HTTPError, got %T: %v", err, err)
	}
	if he.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", he.Code)
	}
}

func TestHandleContributionDelete_SubmittedBlocked(t *testing.T) {
	srv, store := setupContribServer(t)
	id := seedTestContribution(t, store)

	// Update status to "submitted" so the handler should reject deletion.
	if err := store.UpdateContributionStatus(id, "submitted", "https://github.com/example/pr/1"); err != nil {
		t.Fatalf("UpdateContributionStatus: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/contributions/%d/delete", id), nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	err := srv.handleContributionDelete(c)
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("expected *echo.HTTPError, got %T: %v", err, err)
	}
	if he.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", he.Code)
	}
	msg, _ := he.Message.(string)
	if !strings.Contains(msg, "Cannot delete a submitted contribution") {
		t.Errorf("expected message about submitted contribution, got %q", msg)
	}
}

func TestHandleContributionDelete_InvalidID(t *testing.T) {
	srv, _ := setupContribServer(t)

	req := httptest.NewRequest(http.MethodPost, "/contributions/abc/delete", nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("abc")

	err := srv.handleContributionDelete(c)
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("expected *echo.HTTPError, got %T: %v", err, err)
	}
	if he.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", he.Code)
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

func TestHandleContributionDetail_FlashParam(t *testing.T) {
	srv, store := setupContribServer(t)
	id := seedTestContribution(t, store)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/contributions/%d?flash=PR+updated", id), nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	if err := srv.handleContributionDetail(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "PR updated") {
		t.Errorf("expected flash message in body, got:\n%s", body)
	}
	if !strings.Contains(body, "alert-success") {
		t.Errorf("expected alert-success class in body")
	}
}

func TestHandleContributionDetail_FlashWithPRURL(t *testing.T) {
	srv, store := setupContribServer(t)
	id := seedTestContribution(t, store)
	if err := store.UpdateContributionStatus(id, "submitted", "https://github.com/example/repo/pull/42"); err != nil {
		t.Fatalf("UpdateContributionStatus: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/contributions/%d?flash=PR+updated", id), nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	if err := srv.handleContributionDetail(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "View PR on GitHub") {
		t.Errorf("expected 'View PR on GitHub' link in flash, got:\n%s", body)
	}
	if !strings.Contains(body, "https://github.com/example/repo/pull/42") {
		t.Errorf("expected PR URL in flash link, got:\n%s", body)
	}
}

func TestHandleContributionDetail_FlashParamTruncated(t *testing.T) {
	srv, store := setupContribServer(t)
	id := seedTestContribution(t, store)

	longFlash := strings.Repeat("y", 300)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/contributions/%d?flash=%s", id, longFlash), nil)
	rec := httptest.NewRecorder()
	c := srv.echo.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", id))

	if err := srv.handleContributionDetail(c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rec.Body.String()
	if strings.Contains(body, longFlash) {
		t.Errorf("expected long flash to be truncated, but found full string in body")
	}
	truncated := strings.Repeat("y", 200)
	if !strings.Contains(body, truncated) {
		t.Errorf("expected truncated flash (200 chars) to appear in body")
	}
}
