package discdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// graphqlResponse is the shape we write back from the mock server.
type mockResponse struct {
	Data json.RawMessage `json:"data"`
}

func makeDeadpool2Response() json.RawMessage {
	item := MediaItem{
		ID:             "item-1",
		Title:          "Deadpool 2",
		Slug:           "deadpool-2",
		Year:           2018,
		Type:           "Movie",
		RuntimeMinutes: 119,
		ImageURL:       "https://example.com/deadpool2.jpg",
		ExternalIDs: ExternalIDs{
			IMDB: "tt5463162",
			TMDB: "383498",
			TVDB: "",
		},
		Releases: []Release{
			{
				ID:         "release-1",
				Title:      "Deadpool 2 (Blu-ray)",
				Slug:       "deadpool-2-bluray",
				UPC:        "024543547853",
				ASIN:       "B07CXVFK6H",
				ISBN:       "",
				Year:       2018,
				RegionCode: "A",
				Locale:     "en-US",
				ImageURL:   "https://example.com/deadpool2-bluray.jpg",
				Discs: []Disc{
					{
						ID:     "disc-1",
						Index:  0,
						Name:   "Deadpool 2",
						Format: "Blu-ray",
						Slug:   "deadpool-2-disc-1",
						Titles: []DiscTitle{
							{
								ID:         "title-1",
								Index:      0,
								SourceFile: "00001.mpls",
								ItemType:   "Movie",
								HasItem:    true,
								Duration:   "01:59:00",
								Size:       "35000000000",
								SegmentMap: "1,2,3",
								Season:     0,
								Episode:    0,
								Item: &ContentItem{
									Title:   "Deadpool 2",
									Season:  0,
									Episode: 0,
									Type:    "Movie",
								},
							},
						},
					},
				},
			},
		},
	}

	data, _ := json.Marshal(map[string]any{
		"allMediaItems": []MediaItem{item},
	})
	return data
}

func newMockServer(t *testing.T, responseData json.RawMessage) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}

		resp := mockResponse{Data: responseData}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
}

func TestSearchByTitle(t *testing.T) {
	responseData := makeDeadpool2Response()
	srv := newMockServer(t, responseData)
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	items, err := client.SearchByTitle(context.Background(), "Deadpool")
	if err != nil {
		t.Fatalf("SearchByTitle error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0]
	if item.ID != "item-1" {
		t.Errorf("ID: got %q, want %q", item.ID, "item-1")
	}
	if item.Title != "Deadpool 2" {
		t.Errorf("Title: got %q, want %q", item.Title, "Deadpool 2")
	}
	if item.Slug != "deadpool-2" {
		t.Errorf("Slug: got %q, want %q", item.Slug, "deadpool-2")
	}
	if item.Year != 2018 {
		t.Errorf("Year: got %d, want %d", item.Year, 2018)
	}
	if item.Type != "Movie" {
		t.Errorf("Type: got %q, want %q", item.Type, "Movie")
	}
	if item.RuntimeMinutes != 119 {
		t.Errorf("RuntimeMinutes: got %d, want %d", item.RuntimeMinutes, 119)
	}
	if item.ImageURL != "https://example.com/deadpool2.jpg" {
		t.Errorf("ImageURL: got %q, want %q", item.ImageURL, "https://example.com/deadpool2.jpg")
	}
	if item.ExternalIDs.IMDB != "tt5463162" {
		t.Errorf("ExternalIDs.IMDB: got %q, want %q", item.ExternalIDs.IMDB, "tt5463162")
	}
	if item.ExternalIDs.TMDB != "383498" {
		t.Errorf("ExternalIDs.TMDB: got %q, want %q", item.ExternalIDs.TMDB, "383498")
	}

	if len(item.Releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(item.Releases))
	}
	release := item.Releases[0]
	if release.UPC != "024543547853" {
		t.Errorf("Release.UPC: got %q, want %q", release.UPC, "024543547853")
	}
	if release.RegionCode != "A" {
		t.Errorf("Release.RegionCode: got %q, want %q", release.RegionCode, "A")
	}

	if len(release.Discs) != 1 {
		t.Fatalf("expected 1 disc, got %d", len(release.Discs))
	}
	disc := release.Discs[0]
	if disc.Format != "Blu-ray" {
		t.Errorf("Disc.Format: got %q, want %q", disc.Format, "Blu-ray")
	}

	if len(disc.Titles) != 1 {
		t.Fatalf("expected 1 disc title, got %d", len(disc.Titles))
	}
	title := disc.Titles[0]
	if title.SourceFile != "00001.mpls" {
		t.Errorf("DiscTitle.SourceFile: got %q, want %q", title.SourceFile, "00001.mpls")
	}
	if !title.HasItem {
		t.Error("DiscTitle.HasItem: expected true")
	}
	if title.Item == nil {
		t.Fatal("DiscTitle.Item: expected non-nil")
	}
	if title.Item.Title != "Deadpool 2" {
		t.Errorf("ContentItem.Title: got %q, want %q", title.Item.Title, "Deadpool 2")
	}
	if title.Item.Type != "Movie" {
		t.Errorf("ContentItem.Type: got %q, want %q", title.Item.Type, "Movie")
	}
}

func TestSearchByUPC(t *testing.T) {
	responseData := makeDeadpool2Response()
	srv := newMockServer(t, responseData)
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	items, err := client.SearchByUPC(context.Background(), "024543547853")
	if err != nil {
		t.Fatalf("SearchByUPC error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0]
	if item.Title != "Deadpool 2" {
		t.Errorf("Title: got %q, want %q", item.Title, "Deadpool 2")
	}
	if len(item.Releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(item.Releases))
	}
	if item.Releases[0].UPC != "024543547853" {
		t.Errorf("Release.UPC: got %q, want %q", item.Releases[0].UPC, "024543547853")
	}
}

func TestSearchByASIN(t *testing.T) {
	responseData := makeDeadpool2Response()
	srv := newMockServer(t, responseData)
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	items, err := client.SearchByASIN(context.Background(), "B07CXVFK6H")
	if err != nil {
		t.Fatalf("SearchByASIN error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0]
	if item.Title != "Deadpool 2" {
		t.Errorf("Title: got %q, want %q", item.Title, "Deadpool 2")
	}
	if len(item.Releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(item.Releases))
	}
	if item.Releases[0].ASIN != "B07CXVFK6H" {
		t.Errorf("Release.ASIN: got %q, want %q", item.Releases[0].ASIN, "B07CXVFK6H")
	}
}

func TestQueryHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	_, err := client.SearchByTitle(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error from non-200 response")
	}
}

func TestQueryGraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"errors": []map[string]any{
				{"message": "field 'foo' not found"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	_, err := client.SearchByTitle(context.Background(), "anything")
	if err == nil {
		t.Fatal("expected error from GraphQL errors field")
	}
}
