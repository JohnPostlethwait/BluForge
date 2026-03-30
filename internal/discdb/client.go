package discdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const defaultBaseURL = "https://thediscdb.com/graphql/"

// mediaItemFields is the shared GraphQL fragment for media item fields.
const mediaItemFields = `
	id
	title
	slug
	year
	type
	runtimeMinutes
	imageUrl
	externalids {
		imdb
		tmdb
		tvdb
	}
	releases {
		id
		title
		slug
		upc
		asin
		isbn
		year
		regionCode
		locale
		imageUrl
		discs {
			id
			index
			name
			format
			slug
			titles {
				id
				index
				sourceFile
				itemType
				hasItem
				duration
				size
				segmentMap
				season
				episode
				item {
					title
					season
					episode
					type
				}
			}
		}
	}
`

// Client is a GraphQL client for TheDiscDB API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// ClientOption is a functional option for configuring a Client.
type ClientOption func(*Client)

// WithBaseURL sets a custom base URL for the GraphQL endpoint.
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = url
	}
}

// NewClient creates a new TheDiscDB GraphQL client with optional configuration.
func NewClient(opts ...ClientOption) *Client {
	c := &Client{
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type graphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   json.RawMessage  `json:"data"`
	Errors []graphQLError   `json:"errors,omitempty"`
}

type graphQLError struct {
	Message string `json:"message"`
}

// query sends a GraphQL request and returns the raw data field of the response.
func (c *Client) query(ctx context.Context, gql string, vars map[string]any) (json.RawMessage, error) {
	reqBody := graphQLRequest{
		Query:     gql,
		Variables: vars,
	}
	encoded, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("discdb: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("discdb: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discdb: http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("discdb: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discdb: unexpected status %d: %s", resp.StatusCode, body)
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return nil, fmt.Errorf("discdb: unmarshal response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("discdb: graphql error: %s", gqlResp.Errors[0].Message)
	}

	return gqlResp.Data, nil
}

// SearchByTitle searches for media items whose title contains the given string.
func (c *Client) SearchByTitle(ctx context.Context, title string) ([]MediaItem, error) {
	gql := fmt.Sprintf(`
		query SearchByTitle($title: String!) {
			mediaItems(where: { title: { contains: $title } }, first: 50) {
				nodes {
					%s
				}
			}
		}
	`, mediaItemFields)

	data, err := c.query(ctx, gql, map[string]any{"title": title})
	if err != nil {
		return nil, err
	}

	var result struct {
		MediaItems struct {
			Nodes []MediaItem `json:"nodes"`
		} `json:"mediaItems"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("discdb: unmarshal SearchByTitle: %w", err)
	}
	return result.MediaItems.Nodes, nil
}

// SearchByUPC searches for media items that have a release matching the given UPC.
func (c *Client) SearchByUPC(ctx context.Context, upc string) ([]MediaItem, error) {
	gql := fmt.Sprintf(`
		query SearchByUPC($upc: String!) {
			mediaItems(where: { releases: { some: { upc: { eq: $upc } } } }, first: 50) {
				nodes {
					%s
				}
			}
		}
	`, mediaItemFields)

	data, err := c.query(ctx, gql, map[string]any{"upc": upc})
	if err != nil {
		return nil, err
	}

	var result struct {
		MediaItems struct {
			Nodes []MediaItem `json:"nodes"`
		} `json:"mediaItems"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("discdb: unmarshal SearchByUPC: %w", err)
	}
	return result.MediaItems.Nodes, nil
}

// SearchByASIN searches for media items that have a release matching the given ASIN.
func (c *Client) SearchByASIN(ctx context.Context, asin string) ([]MediaItem, error) {
	gql := fmt.Sprintf(`
		query SearchByASIN($asin: String!) {
			mediaItems(where: { releases: { some: { asin: { eq: $asin } } } }, first: 50) {
				nodes {
					%s
				}
			}
		}
	`, mediaItemFields)

	data, err := c.query(ctx, gql, map[string]any{"asin": asin})
	if err != nil {
		return nil, err
	}

	var result struct {
		MediaItems struct {
			Nodes []MediaItem `json:"nodes"`
		} `json:"mediaItems"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("discdb: unmarshal SearchByASIN: %w", err)
	}
	return result.MediaItems.Nodes, nil
}
