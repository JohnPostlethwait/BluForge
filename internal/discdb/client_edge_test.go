package discdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSearchByTitle_EmptyQuery(t *testing.T) {
	// An empty query should still make a valid request. The server returns
	// an empty result set, and the client should handle it gracefully.
	emptyData, _ := json.Marshal(map[string]any{
		"mediaItems": map[string]any{
			"nodes": []MediaItem{},
		},
	})
	srv := newMockServer(t, emptyData)
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	items, err := client.SearchByTitle(context.Background(), "")
	if err != nil {
		t.Fatalf("SearchByTitle with empty query returned error: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("expected 0 items for empty query, got %d", len(items))
	}
}

func TestSearchByTitle_ContextCancellation(t *testing.T) {
	// Create a server that blocks long enough for cancellation to take effect.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until context is cancelled or timeout.
		select {
		case <-r.Context().Done():
			// Client cancelled — the connection will be dropped.
			return
		case <-time.After(10 * time.Second):
			// Should not reach here in a passing test.
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := client.SearchByTitle(ctx, "anything")
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !strings.Contains(err.Error(), "cancel") && !strings.Contains(err.Error(), "context") {
		t.Errorf("expected context cancellation error, got: %v", err)
	}
}

func TestSearchByTitle_MultiWordQuery(t *testing.T) {
	// Verify that multi-word queries use the and-clause approach.
	responseData := makeDeadpool2Response()
	srv := newMockServer(t, responseData)
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	items, err := client.SearchByTitle(context.Background(), "Deadpool 2")
	if err != nil {
		t.Fatalf("SearchByTitle error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Title != "Deadpool 2" {
		t.Errorf("Title: got %q, want %q", items[0].Title, "Deadpool 2")
	}
}

func TestSearchByTitle_AllStopWords(t *testing.T) {
	// When all words are stop words, splitSearchWords returns the original
	// query as a single element, so it should still work.
	emptyData, _ := json.Marshal(map[string]any{
		"mediaItems": map[string]any{
			"nodes": []MediaItem{},
		},
	})
	srv := newMockServer(t, emptyData)
	defer srv.Close()

	client := NewClient(WithBaseURL(srv.URL))
	items, err := client.SearchByTitle(context.Background(), "the a an")
	if err != nil {
		t.Fatalf("SearchByTitle with all stop words returned error: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}
