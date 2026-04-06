package tmdb_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnpostlethwait/bluforge/internal/tmdb"
)

func TestSearch_ReturnsResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/search/movie" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("query") != "Lincoln" {
			t.Errorf("unexpected query: %s", r.URL.Query().Get("query"))
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization: want %q, got %q", "Bearer test-key", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": 65926, "title": "Lincoln", "release_date": "2012-11-09", "poster_path": "/abc.jpg"},
				{"id": 99, "title": "Lincoln (1988)", "release_date": "1988-04-01", "poster_path": ""},
			},
		})
	}))
	defer srv.Close()

	c := tmdb.NewClient("test-key", tmdb.WithBaseURL(srv.URL))
	results, err := c.Search(context.Background(), "Lincoln", "movie")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if results[0].ID != 65926 {
		t.Errorf("ID: want 65926, got %d", results[0].ID)
	}
	if results[0].Title != "Lincoln" {
		t.Errorf("Title: want %q, got %q", "Lincoln", results[0].Title)
	}
	if results[0].Year != 2012 {
		t.Errorf("Year: want 2012, got %d", results[0].Year)
	}
	if results[0].PosterPath != "/abc.jpg" {
		t.Errorf("PosterPath: want %q, got %q", "/abc.jpg", results[0].PosterPath)
	}
}

func TestSearch_TVShow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/search/tv" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization: want %q, got %q", "Bearer test-key", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"id": 1234, "name": "Firefly", "first_air_date": "2002-09-20", "poster_path": "/fire.jpg"},
			},
		})
	}))
	defer srv.Close()

	c := tmdb.NewClient("test-key", tmdb.WithBaseURL(srv.URL))
	results, err := c.Search(context.Background(), "Firefly", "series")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Title != "Firefly" {
		t.Errorf("Title: want %q, got %q", "Firefly", results[0].Title)
	}
	if results[0].Year != 2002 {
		t.Errorf("Year: want 2002, got %d", results[0].Year)
	}
}

func TestSearch_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"results": []any{}})
	}))
	defer srv.Close()

	c := tmdb.NewClient("test-key", tmdb.WithBaseURL(srv.URL))
	results, err := c.Search(context.Background(), "zzznomatch", "movie")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results, got %d", len(results))
	}
}

func TestSearch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"status_message":"Invalid API key"}`))
	}))
	defer srv.Close()

	c := tmdb.NewClient("bad-key", tmdb.WithBaseURL(srv.URL))
	_, err := c.Search(context.Background(), "Lincoln", "movie")
	if err == nil {
		t.Fatal("expected error for HTTP 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status 401, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("error should include response body text, got: %v", err)
	}
}

func TestSearch_InvalidMediaType(t *testing.T) {
	c := tmdb.NewClient("test-key")
	_, err := c.Search(context.Background(), "Lincoln", "tv")
	if err == nil {
		t.Fatal("expected error for unknown mediaType, got nil")
	}
	if !strings.Contains(err.Error(), "unknown mediaType") {
		t.Errorf("error should mention unknown mediaType, got: %v", err)
	}
}

func TestImageURL(t *testing.T) {
	got := tmdb.ImageURL("/abc.jpg", "w92")
	want := "https://image.tmdb.org/t/p/w92/abc.jpg"
	if got != want {
		t.Errorf("ImageURL: want %q, got %q", want, got)
	}
}

func TestImageURL_Empty(t *testing.T) {
	got := tmdb.ImageURL("", "w92")
	if got != "" {
		t.Errorf("ImageURL empty: want %q, got %q", "", got)
	}
}

func TestGetDetails_Movie(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/movie/603" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("append_to_response") != "external_ids" {
			t.Errorf("expected append_to_response=external_ids, got: %s", r.URL.Query().Get("append_to_response"))
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization: want Bearer test-key, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":           603,
			"title":        "The Matrix",
			"overview":     "A computer hacker learns the truth.",
			"tagline":      "Welcome to the Real World.",
			"runtime":      136,
			"release_date": "1999-03-31",
			"poster_path":  "/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg",
			"imdb_id":      "tt0133093",
			"external_ids": map[string]any{"imdb_id": "tt0133093"},
		})
	}))
	defer srv.Close()

	c := tmdb.NewClient("test-key", tmdb.WithBaseURL(srv.URL))
	raw, details, err := c.GetDetails(context.Background(), 603, "movie")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if details.ID != 603 {
		t.Errorf("ID: want 603, got %d", details.ID)
	}
	if details.Title != "The Matrix" {
		t.Errorf("Title: want %q, got %q", "The Matrix", details.Title)
	}
	if details.Overview != "A computer hacker learns the truth." {
		t.Errorf("Overview mismatch")
	}
	if details.Tagline != "Welcome to the Real World." {
		t.Errorf("Tagline mismatch")
	}
	if details.RuntimeMinutes != 136 {
		t.Errorf("RuntimeMinutes: want 136, got %d", details.RuntimeMinutes)
	}
	if details.ReleaseDate != "1999-03-31" {
		t.Errorf("ReleaseDate: want %q, got %q", "1999-03-31", details.ReleaseDate)
	}
	if details.PosterPath != "/f89U3ADr1oiB1s9GkdPOEpXUk5H.jpg" {
		t.Errorf("PosterPath mismatch")
	}
	if details.ImdbID != "tt0133093" {
		t.Errorf("ImdbID: want %q, got %q", "tt0133093", details.ImdbID)
	}
	if len(raw) == 0 {
		t.Error("raw JSON should not be empty")
	}
	var check map[string]any
	if err := json.Unmarshal(raw, &check); err != nil {
		t.Errorf("raw JSON is invalid: %v", err)
	}
}

func TestGetDetails_TV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/3/tv/1396" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":              1396,
			"name":            "Breaking Bad",
			"overview":        "A teacher turned drug lord.",
			"tagline":         "",
			"episode_run_time": []int{47},
			"first_air_date":  "2008-01-20",
			"poster_path":     "/ggFHVNu6YYI5L9pCfOacjizRGt.jpg",
			"external_ids":    map[string]any{"imdb_id": "tt0903747"},
		})
	}))
	defer srv.Close()

	c := tmdb.NewClient("test-key", tmdb.WithBaseURL(srv.URL))
	_, details, err := c.GetDetails(context.Background(), 1396, "series")
	if err != nil {
		t.Fatalf("GetDetails: %v", err)
	}
	if details.Title != "Breaking Bad" {
		t.Errorf("Title: want %q, got %q", "Breaking Bad", details.Title)
	}
	if details.ReleaseDate != "2008-01-20" {
		t.Errorf("ReleaseDate: want %q, got %q", "2008-01-20", details.ReleaseDate)
	}
	if details.ImdbID != "tt0903747" {
		t.Errorf("ImdbID: want %q, got %q", "tt0903747", details.ImdbID)
	}
	if details.RuntimeMinutes != 47 {
		t.Errorf("RuntimeMinutes: want 47, got %d", details.RuntimeMinutes)
	}
}

func TestGetDetails_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"status_message":"The resource you requested could not be found."}`))
	}))
	defer srv.Close()

	c := tmdb.NewClient("test-key", tmdb.WithBaseURL(srv.URL))
	_, _, err := c.GetDetails(context.Background(), 9999999, "movie")
	if err == nil {
		t.Fatal("expected error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should mention 404, got: %v", err)
	}
}

func TestGetDetails_InvalidMediaType(t *testing.T) {
	c := tmdb.NewClient("test-key")
	_, _, err := c.GetDetails(context.Background(), 603, "tv")
	if err == nil {
		t.Fatal("expected error for unknown mediaType, got nil")
	}
	if !strings.Contains(err.Error(), "unknown mediaType") {
		t.Errorf("error should mention unknown mediaType, got: %v", err)
	}
}
