package tmdb_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
