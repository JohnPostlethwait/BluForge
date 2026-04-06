package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/tmdb"
)

// mockTMDBServer returns a test TMDB API server that responds with one movie result.
func mockTMDBServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": 65926, "title": "Lincoln", "release_date": "2012-11-09", "poster_path": "/abc.jpg"},
			},
		})
	}))
}

// setupTMDBServer builds a minimal Server wired to a mock TMDB backend.
func setupTMDBServer(t *testing.T, tmdbSrv *httptest.Server, apiKey string) *Server {
	t.Helper()
	cfg := &config.AppConfig{TMDBApiKey: apiKey}
	s := &Server{
		echo:          echo.New(),
		cfg:           cfg,
		driveSessions: NewDriveSessionStore(),
		sseHub:        NewSSEHub(),
	}
	if tmdbSrv != nil {
		s.tmdbBaseURL = tmdbSrv.URL
	}
	return s
}

func TestHandleTMDBSearch_ReturnsResults(t *testing.T) {
	tmdbSrv := mockTMDBServer(t)
	defer tmdbSrv.Close()

	s := setupTMDBServer(t, tmdbSrv, "test-key")

	req := httptest.NewRequest(http.MethodGet, "/api/tmdb/search?q=Lincoln&type=movie", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	c := s.echo.NewContext(req, rec)

	if err := s.handleTMDBSearch(c); err != nil {
		t.Fatalf("handleTMDBSearch: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d — body: %s", rec.Code, rec.Body.String())
	}

	var results []tmdb.SearchResult
	if err := json.NewDecoder(rec.Body).Decode(&results); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Title != "Lincoln" {
		t.Errorf("Title: want %q, got %q", "Lincoln", results[0].Title)
	}
	if results[0].Year != 2012 {
		t.Errorf("Year: want 2012, got %d", results[0].Year)
	}
}

func TestHandleTMDBSearch_NoKey_Returns503(t *testing.T) {
	s := setupTMDBServer(t, nil, "") // no API key

	req := httptest.NewRequest(http.MethodGet, "/api/tmdb/search?q=Lincoln&type=movie", nil)
	rec := httptest.NewRecorder()
	c := s.echo.NewContext(req, rec)

	err := s.handleTMDBSearch(c)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("want *echo.HTTPError, got %T", err)
	}
	if he.Code != http.StatusServiceUnavailable {
		t.Errorf("status: want 503, got %d", he.Code)
	}
}

func TestHandleTMDBSearch_MissingQuery_Returns400(t *testing.T) {
	s := setupTMDBServer(t, nil, "test-key")

	req := httptest.NewRequest(http.MethodGet, "/api/tmdb/search?type=movie", nil) // no q
	rec := httptest.NewRecorder()
	c := s.echo.NewContext(req, rec)

	err := s.handleTMDBSearch(c)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok {
		t.Fatalf("want *echo.HTTPError, got %T", err)
	}
	if he.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", he.Code)
	}
}

func TestHandleTMDBSearch_DefaultsToMovieType(t *testing.T) {
	tmdbSrv := mockTMDBServer(t)
	defer tmdbSrv.Close()

	s := setupTMDBServer(t, tmdbSrv, "test-key")

	// No &type= param — should default to "movie" without error
	req := httptest.NewRequest(http.MethodGet, "/api/tmdb/search?q=Lincoln", nil)
	rec := httptest.NewRecorder()
	c := s.echo.NewContext(req, rec)

	if err := s.handleTMDBSearch(c); err != nil {
		t.Fatalf("handleTMDBSearch with no type: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", rec.Code)
	}
}
