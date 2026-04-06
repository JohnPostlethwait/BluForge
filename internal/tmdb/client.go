package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.themoviedb.org"

const (
	MediaTypeMovie  = "movie"
	MediaTypeSeries = "series"
)

// MediaDetails holds parsed TMDB movie or TV show details for metadata.json generation.
type MediaDetails struct {
	ID             int
	Title          string
	Overview       string
	Tagline        string
	RuntimeMinutes int
	ReleaseDate    string // YYYY-MM-DD
	PosterPath     string
	ImdbID         string
}

// Fetcher is the interface used by the contribute service for TMDB detail fetching.
type Fetcher interface {
	GetDetails(ctx context.Context, id int, mediaType string) (json.RawMessage, *MediaDetails, error)
}

// Searcher is the subset of Client used by HTTP handlers.
type Searcher interface {
	Search(ctx context.Context, query, mediaType string) ([]SearchResult, error)
}

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
	apiKey     string
	baseURL    string
	httpClient *http.Client
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
		apiKey:     apiKey,
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// GetDetails fetches full TMDB details for a movie or TV show.
// mediaType must be "movie" or "series".
// Returns the raw JSON response (for tmdb.json) and parsed MediaDetails (for metadata.json generation).
func (c *Client) GetDetails(ctx context.Context, id int, mediaType string) (json.RawMessage, *MediaDetails, error) {
	if mediaType != MediaTypeMovie && mediaType != MediaTypeSeries {
		return nil, nil, fmt.Errorf("tmdb: unknown mediaType %q: must be %q or %q", mediaType, MediaTypeMovie, MediaTypeSeries)
	}

	endpoint := fmt.Sprintf("/3/movie/%d", id)
	if mediaType == MediaTypeSeries {
		endpoint = fmt.Sprintf("/3/tv/%d", id)
	}

	u, err := url.Parse(c.baseURL + endpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("tmdb: parse url: %w", err)
	}
	q := u.Query()
	q.Set("append_to_response", "external_ids")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("tmdb: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("tmdb: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, nil, fmt.Errorf("tmdb: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("tmdb: read body: %w", err)
	}

	var parsed struct {
		ID           int    `json:"id"`
		Title        string `json:"title"`
		Name         string `json:"name"`
		Overview     string `json:"overview"`
		Tagline      string `json:"tagline"`
		Runtime      int    `json:"runtime"`
		ReleaseDate  string `json:"release_date"`
		FirstAirDate string `json:"first_air_date"`
		PosterPath   string `json:"poster_path"`
		ImdbID       string `json:"imdb_id"`
		ExternalIDs  struct {
			ImdbID string `json:"imdb_id"`
		} `json:"external_ids"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, nil, fmt.Errorf("tmdb: decode response: %w", err)
	}

	title := parsed.Title
	releaseDate := parsed.ReleaseDate
	imdbID := parsed.ImdbID
	if mediaType == MediaTypeSeries {
		title = parsed.Name
		releaseDate = parsed.FirstAirDate
		imdbID = parsed.ExternalIDs.ImdbID
	}

	details := &MediaDetails{
		ID:             parsed.ID,
		Title:          title,
		Overview:       parsed.Overview,
		Tagline:        parsed.Tagline,
		RuntimeMinutes: parsed.Runtime,
		ReleaseDate:    releaseDate,
		PosterPath:     parsed.PosterPath,
		ImdbID:         imdbID,
	}

	return json.RawMessage(raw), details, nil
}

// Search searches TMDB for movies or TV shows matching query.
// mediaType must be "movie" or "series" (mapped to TMDB's "tv").
func (c *Client) Search(ctx context.Context, query, mediaType string) ([]SearchResult, error) {
	if mediaType != MediaTypeMovie && mediaType != MediaTypeSeries {
		return nil, fmt.Errorf("tmdb: unknown mediaType %q: must be %q or %q", mediaType, MediaTypeMovie, MediaTypeSeries)
	}

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
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("tmdb: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tmdb: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tmdb: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
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
