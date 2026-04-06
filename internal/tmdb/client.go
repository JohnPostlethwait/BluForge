package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const defaultBaseURL = "https://api.themoviedb.org"

// SearchResult is a trimmed TMDB search result.
type SearchResult struct {
	ID         int    `json:"id"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	PosterPath string `json:"posterPath"`
	MediaType  string `json:"mediaType"` // "movie" or "series"
}

// Client is a minimal TMDB API client.
type Client struct {
	apiKey  string
	baseURL string
	http    *http.Client
}

// Option is a functional option for Client.
type Option func(*Client)

// WithBaseURL overrides the TMDB base URL (used in tests).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = u }
}

// NewClient creates a TMDB client with the given API key.
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Search searches TMDB for movies or TV shows matching query.
// mediaType must be "movie" or "series" (mapped to TMDB's "tv").
func (c *Client) Search(ctx context.Context, query, mediaType string) ([]SearchResult, error) {
	endpoint := "/3/search/movie"
	if mediaType == "series" {
		endpoint = "/3/search/tv"
	}

	u, err := url.Parse(c.baseURL + endpoint)
	if err != nil {
		return nil, fmt.Errorf("tmdb: parse url: %w", err)
	}
	q := u.Query()
	q.Set("query", query)
	q.Set("api_key", c.apiKey)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("tmdb: build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tmdb: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tmdb: unexpected status %d", resp.StatusCode)
	}

	var raw struct {
		Results []struct {
			ID           int    `json:"id"`
			Title        string `json:"title"`
			Name         string `json:"name"`
			ReleaseDate  string `json:"release_date"`
			FirstAirDate string `json:"first_air_date"`
			PosterPath   string `json:"poster_path"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("tmdb: decode response: %w", err)
	}

	isSeries := mediaType == "series"
	results := make([]SearchResult, 0, len(raw.Results))
	for _, r := range raw.Results {
		title := r.Title
		dateStr := r.ReleaseDate
		if isSeries {
			title = r.Name
			dateStr = r.FirstAirDate
		}
		year := 0
		if len(dateStr) >= 4 {
			year, _ = strconv.Atoi(dateStr[:4])
		}
		mt := "movie"
		if isSeries {
			mt = "series"
		}
		results = append(results, SearchResult{
			ID:         r.ID,
			Title:      title,
			Year:       year,
			PosterPath: r.PosterPath,
			MediaType:  mt,
		})
	}
	return results, nil
}

// ImageURL returns the full URL for a TMDB poster_path at the given size.
// Common sizes: "w92", "w154", "w185", "w342", "original".
func ImageURL(posterPath, size string) string {
	if posterPath == "" {
		return ""
	}
	return "https://image.tmdb.org/t/p/" + size + posterPath
}
