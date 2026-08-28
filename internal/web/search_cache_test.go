package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/discdb"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
)

// emptyResultServer answers every GraphQL query with no media items, and counts
// how many times it was asked.
func emptyResultServer(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"mediaItems": map[string]any{"nodes": []any{}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func searchCtx(t *testing.T, srv *Server) echo.Context {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/drives/0/search", nil)
	return srv.echo.NewContext(req, httptest.NewRecorder())
}

// "Not in TheDiscDB" was cached for 24 hours alongside real answers. Contribute
// the disc, or wait for someone else to, and BluForge still says there is no
// such release until tomorrow — with no way to make it look again.
//
// A negative result is not a result. It is the absence of one, and it is
// exactly the answer most likely to change.
func TestAnEmptySearchResultIsNotCached(t *testing.T) {
	var hits int32
	api := emptyResultServer(t, &hits)

	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	srv, store := setupDashboardServer(t, mgr)
	srv.discdbClient = discdb.NewClient(discdb.WithBaseURL(api.URL))
	srv.discdbCache = discdb.NewCache(store, time.Hour)

	if got := srv.searchDiscDB(searchCtx(t, srv), "title", "no such film"); len(got) != 0 {
		t.Fatalf("expected no results, got %d", len(got))
	}
	if got := srv.searchDiscDB(searchCtx(t, srv), "title", "no such film"); len(got) != 0 {
		t.Fatalf("expected no results, got %d", len(got))
	}

	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Errorf("TheDiscDB was queried %d time(s) for two searches, want 2 — "+
			"the empty answer was cached and served back", n)
	}
}

// A real answer is still worth caching: that is what the cache is for.
func TestANonEmptySearchResultIsStillCached(t *testing.T) {
	var hits int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"mediaItems": map[string]any{
				"nodes": []any{map[string]any{
					"id": 1, "title": "Deadpool 2", "year": 2018, "type": "movie", "slug": "deadpool-2",
				}},
			}},
		})
	}))
	t.Cleanup(api.Close)

	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	srv, store := setupDashboardServer(t, mgr)
	srv.discdbClient = discdb.NewClient(discdb.WithBaseURL(api.URL))
	srv.discdbCache = discdb.NewCache(store, time.Hour)

	first := srv.searchDiscDB(searchCtx(t, srv), "title", "deadpool")
	if len(first) == 0 {
		t.Fatal("expected a result from the API")
	}
	second := srv.searchDiscDB(searchCtx(t, srv), "title", "deadpool")
	if len(second) != len(first) {
		t.Fatalf("cached search returned %d results, want %d", len(second), len(first))
	}

	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("TheDiscDB was queried %d time(s), want 1 — the real answer should be cached", n)
	}
}
