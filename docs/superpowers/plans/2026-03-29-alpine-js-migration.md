# Alpine.js Frontend Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate dashboard and drive detail pages from pure HTMX to Alpine.js + HTMX, eliminating state management bugs in the search-select-scan-rip workflow.

**Architecture:** Alpine.js manages client-side reactive state via `Alpine.store()`. SSE delivers JSON events that update stores. HTMX handles page navigation and form submissions that redirect. Server hydrates Alpine stores via `<script>` tags on page load. A server-side `DriveSession` map persists selected release metadata across browser refreshes.

**Tech Stack:** Alpine.js (CDN), HTMX 2.0.4 (existing), Go templ templates, Echo HTTP framework, SSE via `EventSource`

**Spec:** `docs/superpowers/specs/2026-03-29-alpine-js-migration-design.md`

---

## File Structure

### New Files
- `internal/web/drive_session.go` — `DriveSession` struct and thread-safe map with get/set/clear methods
- `internal/web/drive_session_test.go` — Unit tests for drive session
- `internal/web/handlers_drive_select.go` — `POST /drives/:id/select` handler
- `internal/web/handlers_drive_select_test.go` — Tests for select handler
- `internal/web/json_helpers.go` — `wantsJSON(c)` content negotiation helper and JSON response structs

### Modified Files
- `templates/layout.templ` — Add Alpine.js CDN script
- `templates/dashboard.templ` — Rewrite to use Alpine.js stores and SSE
- `templates/drive_detail.templ` — Rewrite to use Alpine.js stores, fetch, and SSE
- `internal/web/server.go` — Add `driveSession` field, new route, remove old route
- `internal/web/handlers_dashboard.go` — Return JSON drive list for SSE, remove partial handler
- `internal/web/handlers_drive.go` — Content negotiation on search, hydrate Alpine store on detail page, remove query param state, publish `scan-complete` SSE event
- `internal/web/handlers_drive_test.go` — Update existing tests, add new tests
- `main.go` — Clear drive session on disc eject, publish `scan-complete` event

### Removed Files
- `templates/drive_search_results.templ` — Search results rendered client-side by Alpine
- `templates/drive_search_results_templ.go` — Generated file for above
- `templates/components/drive_card.templ` — Inlined into dashboard Alpine template
- `templates/components/drive_card_templ.go` — Generated file for above

---

## Task 1: Add Alpine.js to Layout and Content Negotiation Helper

**Files:**
- Modify: `templates/layout.templ`
- Create: `internal/web/json_helpers.go`
- Create: `internal/web/json_helpers_test.go`

- [ ] **Step 1: Write the test for `wantsJSON`**

Create `internal/web/json_helpers_test.go`:

```go
package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestWantsJSON_ApplicationJSON(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if !wantsJSON(c) {
		t.Error("expected wantsJSON to return true for Accept: application/json")
	}
}

func TestWantsJSON_TextHTML(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if wantsJSON(c) {
		t.Error("expected wantsJSON to return false for Accept: text/html")
	}
}

func TestWantsJSON_NoHeader(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if wantsJSON(c) {
		t.Error("expected wantsJSON to return false when no Accept header")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestWantsJSON -v`
Expected: FAIL — `wantsJSON` not defined

- [ ] **Step 3: Implement `wantsJSON` and JSON response structs**

Create `internal/web/json_helpers.go`:

```go
package web

import (
	"strings"

	"github.com/labstack/echo/v4"
)

// wantsJSON returns true if the request's Accept header contains "application/json".
func wantsJSON(c echo.Context) bool {
	return strings.Contains(c.Request().Header.Get("Accept"), "application/json")
}

// DriveJSON is the JSON representation of a drive for Alpine.js stores.
type DriveJSON struct {
	Index    int    `json:"index"`
	Name     string `json:"name"`
	DiscName string `json:"discName"`
	State    string `json:"state"`
}

// TitleJSON is the JSON representation of a disc title for Alpine.js stores.
type TitleJSON struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	SourceFile string `json:"sourceFile"`
	Selected   bool   `json:"selected"`
}

// SelectedReleaseJSON is the JSON representation of a user-selected release.
type SelectedReleaseJSON struct {
	MediaItemID string `json:"mediaItemID"`
	ReleaseID   string `json:"releaseID"`
	Title       string `json:"title"`
	Year        string `json:"year"`
	Type        string `json:"type"`
}

// SearchResultJSON is the JSON representation of a search result row.
type SearchResultJSON struct {
	MediaTitle   string `json:"mediaTitle"`
	MediaYear    int    `json:"mediaYear"`
	MediaType    string `json:"mediaType"`
	ReleaseTitle string `json:"releaseTitle"`
	ReleaseUPC   string `json:"releaseUPC"`
	ReleaseASIN  string `json:"releaseASIN"`
	RegionCode   string `json:"regionCode"`
	Format       string `json:"format"`
	DiscCount    int    `json:"discCount"`
	ReleaseID    string `json:"releaseID"`
	MediaItemID  string `json:"mediaItemID"`
}

// DriveStoreJSON is the full Alpine.store('drive') shape for the drive detail page.
type DriveStoreJSON struct {
	DriveIndex      int                  `json:"driveIndex"`
	DriveName       string               `json:"driveName"`
	DiscName        string               `json:"discName"`
	State           string               `json:"state"`
	Scanning        bool                 `json:"scanning"`
	Titles          []TitleJSON          `json:"titles"`
	SelectedRelease *SelectedReleaseJSON `json:"selectedRelease"`
	SearchResults   []SearchResultJSON   `json:"searchResults"`
}

// DrivesStoreJSON is the Alpine.store('drives') shape for the dashboard page.
type DrivesStoreJSON struct {
	Ready bool        `json:"ready"`
	List  []DriveJSON `json:"list"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/web/ -run TestWantsJSON -v`
Expected: PASS

- [ ] **Step 5: Add Alpine.js to layout**

Edit `templates/layout.templ` — add Alpine.js script tag after HTMX:

```templ
<script src="https://unpkg.com/htmx.org@2.0.4" crossorigin="anonymous"></script>
<script src="https://unpkg.com/htmx-ext-sse@2.2.2/sse.js"></script>
<script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.14.9/dist/cdn.min.js"></script>
```

Note: `defer` is required for Alpine.js — it must initialize after the DOM is parsed.

- [ ] **Step 6: Regenerate templ and verify build**

Run: `templ generate && go vet ./...`
Expected: Clean output

- [ ] **Step 7: Commit**

```bash
git add internal/web/json_helpers.go internal/web/json_helpers_test.go templates/layout.templ templates/layout_templ.go
git commit -m "feat: add Alpine.js to layout and content negotiation helper"
```

---

## Task 2: Implement DriveSession Server-Side State

**Files:**
- Create: `internal/web/drive_session.go`
- Create: `internal/web/drive_session_test.go`

- [ ] **Step 1: Write tests for DriveSession**

Create `internal/web/drive_session_test.go`:

```go
package web

import (
	"testing"
)

func TestDriveSessionStore_SetAndGet(t *testing.T) {
	store := NewDriveSessionStore()

	session := &DriveSession{
		MediaItemID: "1",
		ReleaseID:   "10",
		MediaTitle:  "Seinfeld",
		MediaYear:   "1989",
		MediaType:   "Series",
	}

	store.Set(0, session)

	got := store.Get(0)
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if got.MediaTitle != "Seinfeld" {
		t.Errorf("MediaTitle: got %q, want %q", got.MediaTitle, "Seinfeld")
	}
	if got.ReleaseID != "10" {
		t.Errorf("ReleaseID: got %q, want %q", got.ReleaseID, "10")
	}
}

func TestDriveSessionStore_GetMissing(t *testing.T) {
	store := NewDriveSessionStore()

	got := store.Get(99)
	if got != nil {
		t.Errorf("expected nil for missing drive, got %+v", got)
	}
}

func TestDriveSessionStore_Clear(t *testing.T) {
	store := NewDriveSessionStore()

	store.Set(0, &DriveSession{MediaTitle: "Test"})
	store.Clear(0)

	got := store.Get(0)
	if got != nil {
		t.Errorf("expected nil after clear, got %+v", got)
	}
}

func TestDriveSessionStore_SetSearchResults(t *testing.T) {
	store := NewDriveSessionStore()

	store.Set(0, &DriveSession{MediaTitle: "Seinfeld"})

	results := []SearchResultJSON{
		{MediaTitle: "Seinfeld", MediaYear: 1989, ReleaseID: "10"},
		{MediaTitle: "Seinfeld", MediaYear: 1989, ReleaseID: "11"},
	}
	store.SetSearchResults(0, results)

	got := store.Get(0)
	if got == nil {
		t.Fatal("expected session, got nil")
	}
	if len(got.SearchResults) != 2 {
		t.Errorf("SearchResults length: got %d, want 2", len(got.SearchResults))
	}
}

func TestDriveSessionStore_SetSearchResults_NoSession(t *testing.T) {
	store := NewDriveSessionStore()

	results := []SearchResultJSON{
		{MediaTitle: "Test", ReleaseID: "1"},
	}
	store.SetSearchResults(0, results)

	got := store.Get(0)
	if got == nil {
		t.Fatal("expected session created for search results, got nil")
	}
	if len(got.SearchResults) != 1 {
		t.Errorf("SearchResults length: got %d, want 1", len(got.SearchResults))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestDriveSessionStore -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Implement DriveSession**

Create `internal/web/drive_session.go`:

```go
package web

import "sync"

// DriveSession holds transient per-drive workflow state: the user's selected
// release from TheDiscDB and cached search results. This state persists across
// browser refreshes but is cleared when the disc is ejected.
type DriveSession struct {
	MediaItemID   string
	ReleaseID     string
	MediaTitle    string
	MediaYear     string
	MediaType     string
	SearchResults []SearchResultJSON
}

// DriveSessionStore is a thread-safe map of drive index to session state.
type DriveSessionStore struct {
	mu       sync.RWMutex
	sessions map[int]*DriveSession
}

// NewDriveSessionStore creates an empty session store.
func NewDriveSessionStore() *DriveSessionStore {
	return &DriveSessionStore{
		sessions: make(map[int]*DriveSession),
	}
}

// Get returns the session for the given drive index, or nil if none exists.
func (s *DriveSessionStore) Get(driveIndex int) *DriveSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[driveIndex]
}

// Set stores a session for the given drive index.
func (s *DriveSessionStore) Set(driveIndex int, session *DriveSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[driveIndex] = session
}

// Clear removes the session for the given drive index.
func (s *DriveSessionStore) Clear(driveIndex int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, driveIndex)
}

// SetSearchResults stores search results for the given drive index.
// Creates a new session if one does not exist.
func (s *DriveSessionStore) SetSearchResults(driveIndex int, results []SearchResultJSON) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[driveIndex]
	if !ok {
		session = &DriveSession{}
		s.sessions[driveIndex] = session
	}
	session.SearchResults = results
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/web/ -run TestDriveSessionStore -v`
Expected: PASS

- [ ] **Step 5: Wire DriveSessionStore into Server**

Edit `internal/web/server.go`:

Add `driveSessions *DriveSessionStore` field to the `Server` struct:

```go
type Server struct {
	echo         *echo.Echo
	cfgMu        sync.RWMutex
	cfg          *config.AppConfig
	configPath   string
	store        *db.Store
	driveMgr     *drivemanager.Manager
	ripEngine    *ripper.Engine
	discdbClient *discdb.Client
	discdbCache  *discdb.Cache
	sseHub       *SSEHub
	orchestrator *workflow.Orchestrator
	driveSessions *DriveSessionStore
}
```

Initialize in `NewServer`:

```go
s := &Server{
	// ... existing fields ...
	driveSessions: NewDriveSessionStore(),
}
```

- [ ] **Step 6: Run full test suite**

Run: `go test ./... && go vet ./...`
Expected: All pass

- [ ] **Step 7: Commit**

```bash
git add internal/web/drive_session.go internal/web/drive_session_test.go internal/web/server.go
git commit -m "feat: add DriveSessionStore for per-drive workflow state"
```

---

## Task 3: Implement `POST /drives/:id/select` Handler

**Files:**
- Create: `internal/web/handlers_drive_select.go`
- Create: `internal/web/handlers_drive_select_test.go`
- Modify: `internal/web/server.go` (add route)

- [ ] **Step 1: Write the tests**

Create `internal/web/handlers_drive_select_test.go`:

```go
package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/johnpostlethwait/bluforge/internal/config"
	"github.com/johnpostlethwait/bluforge/internal/drivemanager"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)

func TestHandleDriveSelect_PersistsSession(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "TestDisc"}, nil)
	mgr.PollOnce(context.Background())

	cfg := config.AppConfig{OutputDir: "/tmp/test"}
	srv := &Server{
		echo:          echo.New(),
		cfg:           &cfg,
		driveMgr:      mgr,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	srv.echo.POST("/drives/:id/select", srv.handleDriveSelect)

	body := `{"mediaItemID":"1","releaseID":"10","title":"Seinfeld","year":"1989","type":"Series"}`
	req := httptest.NewRequest(http.MethodPost, "/drives/0/select", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify session was persisted.
	session := srv.driveSessions.Get(0)
	if session == nil {
		t.Fatal("expected drive session to be persisted")
	}
	if session.MediaTitle != "Seinfeld" {
		t.Errorf("MediaTitle: got %q, want %q", session.MediaTitle, "Seinfeld")
	}
	if session.ReleaseID != "10" {
		t.Errorf("ReleaseID: got %q, want %q", session.ReleaseID, "10")
	}

	// Verify JSON response.
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	// scanning should be false since no orchestrator is configured.
	if scanning, ok := resp["scanning"].(bool); ok && scanning {
		t.Error("expected scanning=false when no orchestrator configured")
	}
}

func TestHandleDriveSelect_InvalidDrive(t *testing.T) {
	mgr := drivemanager.NewManager(&stubExecutor{}, nil)

	cfg := config.AppConfig{OutputDir: "/tmp/test"}
	srv := &Server{
		echo:          echo.New(),
		cfg:           &cfg,
		driveMgr:      mgr,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	srv.echo.POST("/drives/:id/select", srv.handleDriveSelect)

	body := `{"mediaItemID":"1","releaseID":"10","title":"Test","year":"2020","type":"Movie"}`
	req := httptest.NewRequest(http.MethodPost, "/drives/99/select", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown drive, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestHandleDriveSelect -v`
Expected: FAIL — `handleDriveSelect` not defined (the old one on `handlers_drive.go` has a different signature)

- [ ] **Step 3: Implement the handler**

Create `internal/web/handlers_drive_select.go`:

```go
package web

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v4"
)

// selectRequest is the JSON body for POST /drives/:id/select.
type selectRequest struct {
	MediaItemID string `json:"mediaItemID"`
	ReleaseID   string `json:"releaseID"`
	Title       string `json:"title"`
	Year        string `json:"year"`
	Type        string `json:"type"`
}

// selectResponse is the JSON response for POST /drives/:id/select.
type selectResponse struct {
	Scanning bool        `json:"scanning"`
	Titles   []TitleJSON `json:"titles"`
}

// handleDriveSelect persists the user's release selection in the drive session
// and triggers a disc scan if no cached scan exists.
func (s *Server) handleDriveSelect(c echo.Context) error {
	idx, err := parseDriveIndex(c)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid drive id")
	}

	drv := s.driveMgr.GetDrive(idx)
	if drv == nil {
		return echo.NewHTTPError(http.StatusNotFound, "drive not found")
	}

	var req selectRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}

	// Persist selection in drive session.
	s.driveSessions.Set(idx, &DriveSession{
		MediaItemID:   req.MediaItemID,
		ReleaseID:     req.ReleaseID,
		MediaTitle:    req.Title,
		MediaYear:     req.Year,
		MediaType:     req.Type,
		SearchResults: s.driveSessions.Get(idx).safeSearchResults(),
	})

	resp := selectResponse{}

	// Check for cached scan; trigger background scan if missing.
	if drv.DiscName() != "" && s.orchestrator != nil {
		scan := s.orchestrator.CachedScan(idx, drv.DiscName())
		if scan == nil {
			resp.Scanning = true
			go func() {
				bgCtx := context.Background()
				result, err := s.orchestrator.ScanDisc(bgCtx, idx)
				if err != nil {
					slog.Error("background disc scan failed", "drive_index", idx, "error", err)
					return
				}
				// Publish scan-complete SSE event.
				titles := scanToTitleJSON(result)
				s.broadcastScanComplete(idx, titles)
			}()
		} else {
			resp.Titles = scanToTitleJSON(scan)
		}
	}

	return c.JSON(http.StatusOK, resp)
}

// safeSearchResults returns the search results from a session, or nil if the session is nil.
func (ds *DriveSession) safeSearchResults() []SearchResultJSON {
	if ds == nil {
		return nil
	}
	return ds.SearchResults
}
```

- [ ] **Step 4: Add helper functions for scan-to-JSON conversion and SSE broadcast**

Add to `internal/web/json_helpers.go`:

```go
// scanToTitleJSON converts a makemkv.DiscScan's titles into TitleJSON slices.
func scanToTitleJSON(scan *makemkv.DiscScan) []TitleJSON {
	titles := make([]TitleJSON, 0, len(scan.Titles))
	for _, t := range scan.Titles {
		titles = append(titles, TitleJSON{
			Index:      t.Index,
			Name:       t.Name(),
			Duration:   t.Duration(),
			Size:       t.SizeHuman(),
			SourceFile: t.SourceFile(),
			Selected:   true,
		})
	}
	return titles
}
```

Add the import for `makemkv` at the top of `json_helpers.go`:

```go
import (
	"encoding/json"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
)
```

Add `broadcastScanComplete` to `internal/web/json_helpers.go`:

```go
// broadcastScanComplete publishes a scan-complete SSE event with title data.
func (s *Server) broadcastScanComplete(driveIndex int, titles []TitleJSON) {
	payload := struct {
		DriveIndex int         `json:"driveIndex"`
		Titles     []TitleJSON `json:"titles"`
	}{
		DriveIndex: driveIndex,
		Titles:     titles,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.sseHub.Broadcast(SSEEvent{Event: "scan-complete", Data: string(data)})
}
```

- [ ] **Step 5: Add the route**

Edit `internal/web/server.go` — add route after the existing search route:

```go
e.POST("/drives/:id/search", s.handleDriveSearch)
e.POST("/drives/:id/select", s.handleDriveSelect)
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/web/ -run TestHandleDriveSelect -v`
Expected: PASS

- [ ] **Step 7: Run full test suite**

Run: `go test ./... && go vet ./...`
Expected: All pass

- [ ] **Step 8: Commit**

```bash
git add internal/web/handlers_drive_select.go internal/web/handlers_drive_select_test.go internal/web/json_helpers.go internal/web/server.go
git commit -m "feat: add POST /drives/:id/select endpoint with session persistence"
```

---

## Task 4: Update Search Handler for Content Negotiation

**Files:**
- Modify: `internal/web/handlers_drive.go`
- Modify: `internal/web/handlers_drive_test.go`

- [ ] **Step 1: Write test for JSON search response**

Add to `internal/web/handlers_drive_test.go`:

```go
func TestHandleDriveSearch_JSONResponse(t *testing.T) {
	// Set up a mock DiscDB server that returns results.
	responseData := makeSearchResponseData()
	discdbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]json.RawMessage{"data": responseData})
	}))
	defer discdbSrv.Close()

	mgr := drivemanager.NewManager(&stubExecutor{}, nil)
	cfg := config.AppConfig{OutputDir: "/tmp/test"}
	client := discdb.NewClient(discdb.WithBaseURL(discdbSrv.URL))

	srv := &Server{
		echo:          echo.New(),
		cfg:           &cfg,
		driveMgr:      mgr,
		discdbClient:  client,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	srv.echo.POST("/drives/:id/search", srv.handleDriveSearch)

	form := url.Values{}
	form.Set("query", "Seinfeld")

	req := httptest.NewRequest(http.MethodPost, "/drives/0/search", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("expected JSON content type, got %q", contentType)
	}

	var results []SearchResultJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatalf("failed to parse JSON response: %v", err)
	}
}

// makeSearchResponseData builds a minimal DiscDB GraphQL response for testing.
func makeSearchResponseData() json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"mediaItems": map[string]any{
			"nodes": []map[string]any{
				{
					"id": 1, "title": "Seinfeld", "year": 1989, "type": "Series",
					"slug": "seinfeld", "runtimeMinutes": 22, "imageUrl": "",
					"externalids": map[string]any{"imdb": "", "tmdb": "", "tvdb": ""},
					"releases": []map[string]any{
						{
							"id": 10, "title": "Complete Series", "slug": "complete",
							"upc": "043396641280", "asin": "", "isbn": "", "year": 2020,
							"regionCode": "A", "locale": "en-US", "imageUrl": "",
							"discs": []map[string]any{
								{"id": 100, "index": 0, "name": "Disc 1", "format": "UHD",
									"slug": "disc-1", "contentHash": "", "titles": []any{}},
							},
						},
					},
				},
			},
		},
	})
	return data
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/ -run TestHandleDriveSearch_JSONResponse -v`
Expected: FAIL — search handler doesn't check Accept header yet

- [ ] **Step 3: Update `handleDriveSearch` for content negotiation**

Edit `internal/web/handlers_drive.go`. Update the `handleDriveSearch` function. After the search results are obtained, check Accept header before returning:

Replace the return statement at the end of `handleDriveSearch`:

```go
	// Old code (remove):
	// return templates.DriveSearchResults(idx, rows, searchErr).Render(...)

	// New: content negotiation
	if wantsJSON(c) {
		if items == nil && searchErr == "" {
			return c.JSON(http.StatusOK, []SearchResultJSON{})
		}
		if searchErr != "" {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": searchErr})
		}
		jsonRows := mediaItemsToSearchJSON(items)
		// Cache search results in drive session.
		s.driveSessions.SetSearchResults(idx, jsonRows)
		return c.JSON(http.StatusOK, jsonRows)
	}

	// HTML fallback (for non-Alpine consumers).
	rows := mediaItemsToRows(items)
	return templates.DriveSearchResults(idx, rows, searchErr).Render(c.Request().Context(), c.Response().Writer)
```

Also remove the old select-flow detection code (the `releaseID`/`mediaItemID` check at the top of `handleDriveSearch`) — that's now handled by the dedicated `/drives/:id/select` endpoint.

- [ ] **Step 4: Add `mediaItemsToSearchJSON` helper**

Add to `internal/web/json_helpers.go`:

```go
// mediaItemsToSearchJSON converts MediaItems to SearchResultJSON rows.
func mediaItemsToSearchJSON(items []discdb.MediaItem) []SearchResultJSON {
	var rows []SearchResultJSON
	for _, item := range items {
		for _, rel := range item.Releases {
			format := ""
			if len(rel.Discs) > 0 {
				format = rel.Discs[0].Format
			}
			rows = append(rows, SearchResultJSON{
				MediaTitle:   item.Title,
				MediaYear:    item.Year,
				MediaType:    item.Type,
				ReleaseTitle: rel.Title,
				ReleaseUPC:   rel.UPC,
				ReleaseASIN:  rel.ASIN,
				RegionCode:   rel.RegionCode,
				Format:       format,
				DiscCount:    len(rel.Discs),
				ReleaseID:    strconv.Itoa(rel.ID),
				MediaItemID:  strconv.Itoa(item.ID),
			})
		}
	}
	if rows == nil {
		rows = []SearchResultJSON{}
	}
	return rows
}
```

Add `strconv` and `discdb` imports to `json_helpers.go`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/web/ -run TestHandleDriveSearch -v`
Expected: All PASS

- [ ] **Step 6: Run full test suite**

Run: `go test ./... && go vet ./...`
Expected: All pass

- [ ] **Step 7: Commit**

```bash
git add internal/web/handlers_drive.go internal/web/handlers_drive_test.go internal/web/json_helpers.go
git commit -m "feat: add content negotiation to search handler (JSON for Alpine, HTML fallback)"
```

---

## Task 5: Update Drive Detail Handler to Hydrate Alpine Store

**Files:**
- Modify: `internal/web/handlers_drive.go`
- Modify: `templates/drive_detail.templ`

- [ ] **Step 1: Update `DriveDetailData` struct**

Edit `templates/drive_detail.templ`. Replace the `DriveDetailData` struct and remove the old `Selected*` fields. Add a single `StoreJSON` field:

```go
// DriveDetailData holds all data needed to render the drive detail page.
type DriveDetailData struct {
	DriveIndex     int
	DriveName      string
	DiscName       string
	State          string
	Titles         []TitleRow
	MatchedMedia   string
	MatchedRelease string
	HasMapping     bool
	Scanning       bool
	Error          string
	StoreJSON      string // JSON blob for Alpine.store('drive') hydration
}
```

Remove the `SelectedMediaItemID`, `SelectedReleaseID`, `SelectedMediaTitle`, `SelectedMediaYear`, `SelectedMediaType` fields.

- [ ] **Step 2: Update `handleDriveDetail` to build store JSON**

Edit `internal/web/handlers_drive.go`. In `handleDriveDetail`, build the Alpine store JSON and set it on the data struct. Remove all query param state reading. After building the existing `data` struct and populating titles/mapping:

```go
	// Build Alpine store hydration JSON.
	driveStore := DriveStoreJSON{
		DriveIndex: idx,
		DriveName:  drv.DriveName(),
		DiscName:   drv.DiscName(),
		State:      string(drv.State()),
		Scanning:   data.Scanning,
		Titles:     make([]TitleJSON, 0),
		SearchResults: make([]SearchResultJSON, 0),
	}

	for _, t := range data.Titles {
		driveStore.Titles = append(driveStore.Titles, TitleJSON{
			Index:      t.Index,
			Name:       t.Name,
			Duration:   t.Duration,
			Size:       t.Size,
			SourceFile: t.SourceFile,
			Selected:   t.Selected,
		})
	}

	// Hydrate from drive session if available.
	if session := s.driveSessions.Get(idx); session != nil {
		driveStore.SelectedRelease = &SelectedReleaseJSON{
			MediaItemID: session.MediaItemID,
			ReleaseID:   session.ReleaseID,
			Title:       session.MediaTitle,
			Year:        session.MediaYear,
			Type:        session.MediaType,
		}
		driveStore.SearchResults = session.SearchResults
		if driveStore.SearchResults == nil {
			driveStore.SearchResults = make([]SearchResultJSON, 0)
		}
	}

	storeBytes, _ := json.Marshal(driveStore)
	data.StoreJSON = string(storeBytes)
```

Remove the old query param reading block (`if mid := c.QueryParam("media_item_id"); mid != "" { ... }`).

- [ ] **Step 3: Regenerate templ and verify build**

Run: `templ generate && go vet ./...`
Expected: Clean (template changes will come in Task 7)

- [ ] **Step 4: Run full test suite**

Run: `go test ./...`
Expected: All pass (template rendering still works, just with new field)

- [ ] **Step 5: Commit**

```bash
git add internal/web/handlers_drive.go templates/drive_detail.templ templates/drive_detail_templ.go
git commit -m "feat: hydrate Alpine store JSON in drive detail handler"
```

---

## Task 6: Update SSE Events and Clear Session on Eject

**Files:**
- Modify: `main.go`
- Modify: `internal/web/server.go`

- [ ] **Step 1: Add `ClearDriveSession` method to Server**

Edit `internal/web/server.go`. Add a public method:

```go
// ClearDriveSession removes the drive session for the given index.
// Called when a disc is ejected to clear stale selection state.
func (s *Server) ClearDriveSession(driveIndex int) {
	s.driveSessions.Clear(driveIndex)
}
```

- [ ] **Step 2: Add `BroadcastScanComplete` method to Server**

Edit `internal/web/server.go`. Add a public method so `main.go` can trigger it:

```go
// BroadcastScanComplete publishes a scan-complete SSE event.
func (s *Server) BroadcastScanComplete(driveIndex int, titles []TitleJSON) {
	s.broadcastScanComplete(driveIndex, titles)
}
```

- [ ] **Step 3: Broadcast `drive-update` with full drive list on drive events**

Edit `main.go`. In the drive event callback, after the existing `drive-event` broadcast, add a `drive-update` event with the full drive list JSON for the dashboard:

```go
	// Broadcast drive-update with full drive list for dashboard Alpine store.
	allDrives := driveMgr.GetAllDrives()
	driveList := make([]web.DriveJSON, 0, len(allDrives))
	for _, dsm := range allDrives {
		driveList = append(driveList, web.DriveJSON{
			Index:    dsm.Index(),
			Name:     dsm.DriveName(),
			DiscName: dsm.DiscName(),
			State:    string(dsm.State()),
		})
	}
	driveUpdatePayload := struct {
		Ready bool           `json:"ready"`
		List  []web.DriveJSON `json:"list"`
	}{Ready: driveMgr.Ready(), List: driveList}
	driveUpdateData, _ := json.Marshal(driveUpdatePayload)
	sseHub.Broadcast(web.SSEEvent{Event: "drive-update", Data: string(driveUpdateData)})
```

- [ ] **Step 4: Clear drive session on disc eject**

Edit `main.go`. In the drive event callback, after the existing `InvalidateScan` call for eject events:

```go
	if ev.Type == drivemanager.EventDiscEjected || ev.Type == drivemanager.EventDiscInserted {
		orch.InvalidateScan(ev.DriveIndex)
	}

	// Clear drive session on eject so stale selection state doesn't persist.
	if ev.Type == drivemanager.EventDiscEjected && srv != nil {
		srv.ClearDriveSession(ev.DriveIndex)
	}
```

- [ ] **Step 5: Publish scan-complete from orchestrator background scans**

Edit `internal/web/handlers_drive.go`. In `handleDriveDetail`, where the background scan goroutine runs, add the SSE broadcast after the scan completes:

```go
	go func() {
		bgCtx := context.Background()
		result, scanErr := s.orchestrator.ScanDisc(bgCtx, idx)
		if scanErr != nil {
			slog.Error("background disc scan failed", "drive_index", idx, "error", scanErr)
			return
		}
		titles := scanToTitleJSON(result)
		s.broadcastScanComplete(idx, titles)
	}()
```

- [ ] **Step 6: Run full test suite**

Run: `go test ./... && go vet ./...`
Expected: All pass

- [ ] **Step 7: Commit**

```bash
git add main.go internal/web/server.go internal/web/handlers_drive.go internal/web/json_helpers.go
git commit -m "feat: broadcast drive-update and scan-complete SSE events as JSON"
```

---

## Task 7: Rewrite Dashboard Template with Alpine.js

**Files:**
- Modify: `templates/dashboard.templ`
- Modify: `internal/web/handlers_dashboard.go`
- Remove: `templates/components/drive_card.templ` (functionality inlined)

- [ ] **Step 1: Rewrite `dashboard.templ`**

Replace the entire content of `templates/dashboard.templ`:

```templ
package templates

import "encoding/json"

// DashboardData holds the data needed to render the dashboard page.
type DashboardData struct {
	StoreJSON string // JSON blob for Alpine.store('drives') hydration
}

templ Dashboard(data DashboardData) {
	@Layout("Dashboard") {
		<script>
			document.addEventListener('alpine:init', () => {
				Alpine.store('drives', @templ.JSONScript(data.StoreJSON))

				const evtSource = new EventSource('/events')
				evtSource.addEventListener('drive-update', (e) => {
					const update = JSON.parse(e.data)
					Alpine.store('drives').ready = update.ready
					Alpine.store('drives').list = update.list
				})
			})
		</script>

		<div class="page-header">
			<h1>Drives</h1>
		</div>

		<div class="drive-grid" x-data>
			<template x-if="!$store.drives.ready">
				<div class="empty-state">
					<p>Scanning for drives…</p>
				</div>
			</template>

			<template x-if="$store.drives.ready && $store.drives.list.length === 0">
				<div class="empty-state">
					<p>No drives detected. Make sure MakeMKV is installed and a drive is connected.</p>
				</div>
			</template>

			<template x-for="drive in $store.drives.list" :key="drive.index">
				<div class="card" :id="'drive-' + drive.index">
					<div class="card-header">
						<span class="card-title" x-text="drive.name"></span>
						<span :class="'badge badge-' + drive.state" x-text="drive.state"></span>
					</div>
					<template x-if="drive.discName">
						<p class="text-secondary" x-text="drive.discName"></p>
					</template>
					<template x-if="!drive.discName">
						<p class="text-muted">No disc</p>
					</template>
					<template x-if="drive.discName">
						<div class="mt-3">
							<a :href="'/drives/' + drive.index" class="btn btn-secondary btn-sm">Details</a>
						</div>
					</template>
				</div>
			</template>
		</div>
	}
}
```

Note: `@templ.JSONScript` safely injects JSON into a `<script>` tag. If templ doesn't support this helper, use a raw string injection: the `StoreJSON` is server-controlled and safe.

- [ ] **Step 2: Check if templ supports `@templ.JSONScript`**

If it doesn't, replace the store initialization with a pattern that works. The safe approach is to use a `<script type="application/json">` data island:

```templ
<script type="application/json" id="drives-data">
	{ data.StoreJSON }
</script>
<script>
	document.addEventListener('alpine:init', () => {
		const raw = document.getElementById('drives-data').textContent
		Alpine.store('drives', JSON.parse(raw))

		const evtSource = new EventSource('/events')
		evtSource.addEventListener('drive-update', (e) => {
			const update = JSON.parse(e.data)
			Alpine.store('drives').ready = update.ready
			Alpine.store('drives').list = update.list
		})
	})
</script>
```

Use whichever approach compiles with templ.

- [ ] **Step 3: Update `handleDashboard`**

Edit `internal/web/handlers_dashboard.go`. Replace the handler to build the store JSON instead of card data:

```go
func (s *Server) handleDashboard(c echo.Context) error {
	drives := s.driveMgr.GetAllDrives()

	driveList := make([]DriveJSON, 0, len(drives))
	for _, dsm := range drives {
		driveList = append(driveList, DriveJSON{
			Index:    dsm.Index(),
			Name:     dsm.DriveName(),
			DiscName: dsm.DiscName(),
			State:    string(dsm.State()),
		})
	}

	storeData := DrivesStoreJSON{
		Ready: s.driveMgr.Ready(),
		List:  driveList,
	}
	storeBytes, _ := json.Marshal(storeData)

	data := templates.DashboardData{StoreJSON: string(storeBytes)}
	return templates.Dashboard(data).Render(c.Request().Context(), c.Response().Writer)
}
```

Remove `handleDrivesPartial` — it's no longer needed.

Update imports: add `"encoding/json"`, remove `"github.com/johnpostlethwait/bluforge/templates/components"`.

- [ ] **Step 4: Remove `/drives-partial` route from server.go**

Edit `internal/web/server.go` — delete the line:

```go
e.GET("/drives-partial", s.handleDrivesPartial)
```

- [ ] **Step 5: Regenerate templ and verify build**

Run: `templ generate && go build -o /dev/null . && go vet ./...`
Expected: Clean build

- [ ] **Step 6: Update dashboard tests**

Edit `internal/web/handlers_drive_test.go`. Remove `TestHandleDrivesPartial` and `TestHandleDrivesPartial_NotReady`. Replace with:

```go
func TestHandleDashboard_JSONStore(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "TestDisc"}, nil)
	mgr.PollOnce(context.Background())

	cfg := config.AppConfig{OutputDir: "/tmp/test"}
	srv := &Server{
		echo:          echo.New(),
		cfg:           &cfg,
		driveMgr:      mgr,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	srv.echo.GET("/", srv.handleDashboard)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	// Should contain the Alpine store initialization with drive data.
	if !strings.Contains(body, "Alpine.store") {
		t.Error("response should contain Alpine.store initialization")
	}
	if !strings.Contains(body, "TestDisc") {
		t.Error("response should contain disc name in store JSON")
	}
	if !strings.Contains(body, "drive-update") {
		t.Error("response should contain SSE event listener for drive-update")
	}
}
```

- [ ] **Step 7: Run full test suite**

Run: `go test ./... && go vet ./...`
Expected: All pass

- [ ] **Step 8: Commit**

```bash
git add templates/dashboard.templ templates/dashboard_templ.go internal/web/handlers_dashboard.go internal/web/handlers_drive_test.go internal/web/server.go
git commit -m "feat: rewrite dashboard with Alpine.js reactive store and SSE"
```

---

## Task 8: Rewrite Drive Detail Template with Alpine.js

**Files:**
- Modify: `templates/drive_detail.templ`

- [ ] **Step 1: Rewrite `drive_detail.templ`**

Replace the template body in `templates/drive_detail.templ`. Keep the struct definitions at the top (`TitleRow`, `DriveDetailData`). Replace the `templ DriveDetail` function:

```templ
templ DriveDetail(data DriveDetailData) {
	@Layout(data.DiscName) {
		<script type="application/json" id="drive-data">
			{ data.StoreJSON }
		</script>
		<script>
			document.addEventListener('alpine:init', () => {
				const raw = document.getElementById('drive-data').textContent
				Alpine.store('drive', JSON.parse(raw))

				const evtSource = new EventSource('/events')

				evtSource.addEventListener('scan-complete', (e) => {
					const update = JSON.parse(e.data)
					if (update.driveIndex === Alpine.store('drive').driveIndex) {
						Alpine.store('drive').scanning = false
						Alpine.store('drive').titles = update.titles
					}
				})

				evtSource.addEventListener('drive-event', (e) => {
					const update = JSON.parse(e.data)
					if (update.driveIndex === Alpine.store('drive').driveIndex) {
						Alpine.store('drive').state = update.state
						Alpine.store('drive').discName = update.discName
						if (update.type === 'disc_ejected') {
							Alpine.store('drive').titles = []
							Alpine.store('drive').selectedRelease = null
							Alpine.store('drive').searchResults = []
							Alpine.store('drive').scanning = false
						}
					}
				})

				evtSource.addEventListener('rip-update', (e) => {
					const update = JSON.parse(e.data)
					if (update.DriveIndex === Alpine.store('drive').driveIndex) {
						Alpine.store('drive').ripProgress = update
					}
				})
			})

			async function searchDiscDB(driveIndex) {
				const query = document.getElementById('search-query').value.trim()
				if (!query) return

				const form = new FormData()
				form.append('query', query)

				const resp = await fetch('/drives/' + driveIndex + '/search', {
					method: 'POST',
					headers: { 'Accept': 'application/json' },
					body: form,
				})

				if (resp.ok) {
					Alpine.store('drive').searchResults = await resp.json()
				}
			}

			async function selectRelease(driveIndex, result) {
				Alpine.store('drive').selectedRelease = {
					mediaItemID: result.mediaItemID,
					releaseID: result.releaseID,
					title: result.mediaTitle,
					year: String(result.mediaYear),
					type: result.mediaType,
				}

				const resp = await fetch('/drives/' + driveIndex + '/select', {
					method: 'POST',
					headers: {
						'Content-Type': 'application/json',
						'Accept': 'application/json',
					},
					body: JSON.stringify({
						mediaItemID: result.mediaItemID,
						releaseID: result.releaseID,
						title: result.mediaTitle,
						year: String(result.mediaYear),
						type: result.mediaType,
					}),
				})

				if (resp.ok) {
					const data = await resp.json()
					Alpine.store('drive').scanning = data.scanning
					if (data.titles && data.titles.length > 0) {
						Alpine.store('drive').titles = data.titles
					}
				}
			}
		</script>

		if data.Error != "" {
			<div class="alert alert-error">
				{ data.Error }
			</div>
		}

		<div class="page-header">
			<h1>{ data.DriveName }</h1>
			<a href="/" class="btn btn-secondary btn-sm">← Back</a>
		</div>

		<!-- Disc Info Card -->
		<div class="card" style="margin-bottom: 1rem;">
			<div class="card-header">
				<span class="card-title">Disc Info</span>
				<span x-data :class="'badge badge-' + $store.drive.state" x-text="$store.drive.state"></span>
			</div>
			<p class="text-secondary" x-data x-text="$store.drive.discName"></p>
		</div>

		<!-- Search Card -->
		<div class="card" style="margin-bottom: 1rem;" x-data>
			<div class="section-title">Search TheDiscDB</div>
			<form @submit.prevent={ fmt.Sprintf("searchDiscDB(%d)", data.DriveIndex) }>
				<div class="flex gap-2 items-center">
					<input type="search" id="search-query" placeholder="Search by title, UPC, ASIN…"
						x-init={ fmt.Sprintf("$el.value = $store.drive.discName") }
						style="flex:1;"/>
					<button type="submit" class="btn btn-primary btn-sm">Search</button>
				</div>
			</form>

			<!-- Search Results -->
			<div class="mt-3">
				<template x-if="$store.drive.searchResults.length > 0">
					<div style="overflow-x:auto;">
						<table>
							<thead>
								<tr>
									<th>Title</th>
									<th>Year</th>
									<th>Type</th>
									<th>Release</th>
									<th>UPC</th>
									<th>Format</th>
									<th></th>
								</tr>
							</thead>
							<tbody>
								<template x-for="r in $store.drive.searchResults" :key="r.releaseID">
									<tr>
										<td x-text="r.mediaTitle"></td>
										<td class="text-secondary" x-text="r.mediaYear"></td>
										<td><span class="badge badge-identified" x-text="r.mediaType"></span></td>
										<td class="text-secondary" x-text="r.releaseTitle"></td>
										<td class="text-muted" x-text="r.releaseUPC"></td>
										<td class="text-secondary" x-text="r.format"></td>
										<td>
											<button class="btn btn-primary btn-sm"
												@click={ fmt.Sprintf("selectRelease(%d, r)", data.DriveIndex) }
											>Select</button>
										</td>
									</tr>
								</template>
							</tbody>
						</table>
					</div>
				</template>
			</div>
		</div>

		<!-- Matched Release Banner -->
		<template x-data x-if="$store.drive.selectedRelease">
			<div class="card" style="margin-bottom: 1rem;">
				<div class="alert alert-success">
					Matched: <span x-text="$store.drive.selectedRelease.title"></span>
					(<span x-text="$store.drive.selectedRelease.year"></span>)
					— <span x-text="$store.drive.selectedRelease.type"></span>
				</div>
			</div>
		</template>

		<!-- Titles Card -->
		<div class="card" x-data>
			<div class="section-title">Titles</div>

			<template x-if="$store.drive.scanning">
				<div class="empty-state">
					<p>Scanning disc…</p>
				</div>
			</template>

			<template x-if="!$store.drive.scanning && $store.drive.titles.length === 0">
				<div class="empty-state">
					<p>No titles found. Insert a disc or wait for scanning to complete.</p>
				</div>
			</template>

			<template x-if="!$store.drive.scanning && $store.drive.titles.length > 0">
				<form hx-post={ fmt.Sprintf("/drives/%d/rip", data.DriveIndex) } hx-target="body" hx-swap="outerHTML">
					<!-- Hidden fields populated from Alpine store -->
					<input type="hidden" name="disc_name" x-bind:value="$store.drive.discName"/>
					<input type="hidden" name="media_item_id"
						x-bind:value="$store.drive.selectedRelease ? $store.drive.selectedRelease.mediaItemID : ''"/>
					<input type="hidden" name="release_id"
						x-bind:value="$store.drive.selectedRelease ? $store.drive.selectedRelease.releaseID : ''"/>
					<input type="hidden" name="content_title"
						x-bind:value="$store.drive.selectedRelease ? $store.drive.selectedRelease.title : ''"/>
					<input type="hidden" name="content_year"
						x-bind:value="$store.drive.selectedRelease ? $store.drive.selectedRelease.year : ''"/>
					<input type="hidden" name="content_type"
						x-bind:value="$store.drive.selectedRelease ? $store.drive.selectedRelease.type : ''"/>

					<div style="overflow-x:auto;">
						<table>
							<thead>
								<tr>
									<th></th>
									<th>#</th>
									<th>Name</th>
									<th>Duration</th>
									<th>Size</th>
								</tr>
							</thead>
							<tbody>
								<template x-for="t in $store.drive.titles" :key="t.index">
									<tr>
										<td><input type="checkbox" name="titles" :value="t.index" :checked="t.selected"/></td>
										<td class="text-muted" x-text="t.index"></td>
										<td x-text="t.name"></td>
										<td class="text-secondary" x-text="t.duration"></td>
										<td class="text-secondary" x-text="t.size"></td>
									</tr>
								</template>
							</tbody>
						</table>
					</div>
					<div class="mt-3">
						<button type="submit" class="btn btn-primary">Rip Selected</button>
					</div>
				</form>
			</template>
		</div>
	}
}
```

- [ ] **Step 2: Regenerate templ and verify build**

Run: `templ generate && go build -o /dev/null . && go vet ./...`
Expected: Clean build. Note: templ may flag some Alpine syntax. If `@submit.prevent` or `@click` conflict with templ's `@` syntax for component calls, wrap them in raw HTML attributes using templ's attribute syntax.

- [ ] **Step 3: Fix any templ compilation issues**

If templ can't handle `@submit.prevent` or `@click` (since `@` is templ's component call syntax), use the `x-on:` prefix instead:

- `@submit.prevent` → `x-on:submit.prevent`
- `@click` → `x-on:click`

- [ ] **Step 4: Run full test suite**

Run: `go test ./...`
Expected: All pass

- [ ] **Step 5: Commit**

```bash
git add templates/drive_detail.templ templates/drive_detail_templ.go
git commit -m "feat: rewrite drive detail page with Alpine.js reactive store"
```

---

## Task 9: Clean Up Removed Files and Old Code

**Files:**
- Remove: `templates/drive_search_results.templ`
- Remove: `templates/drive_search_results_templ.go`
- Modify: `internal/web/handlers_drive.go` — remove old `handleDriveSelect` (the one with `echo.Context, int, string, string` signature)
- Modify: `internal/web/handlers_drive_test.go` — remove obsolete tests

- [ ] **Step 1: Remove `drive_search_results.templ` and generated file**

Ask user for permission, then remove:
- `templates/drive_search_results.templ`
- `templates/drive_search_results_templ.go`

- [ ] **Step 2: Remove old `handleDriveSelect` from `handlers_drive.go`**

Edit `internal/web/handlers_drive.go`. Delete the old `handleDriveSelect` function (the one that takes `c echo.Context, idx int, mediaItemID, releaseID string`) and the code in `handleDriveSearch` that called it. The new `handleDriveSelect` in `handlers_drive_select.go` replaces it.

Also remove the import for `templates` if it's no longer used in `handlers_drive.go` after removing the HTML fallback from search (check if `templates.DriveSearchResults` is still referenced — if not, remove the import).

- [ ] **Step 3: Update tests**

Edit `internal/web/handlers_drive_test.go`:

- Remove `TestHandleDriveSearch_SelectFlow` (the old HTMX select flow is gone)
- Remove `TestHandleDriveSearch_QueryFlow_NoClient` or update it to check JSON response
- Remove `TestHandleDriveSearch_EmptyQuery` or update to check JSON response
- Keep `TestMediaItemsToRows` tests (still used for HTML fallback)

- [ ] **Step 4: Verify build and tests**

Run: `templ generate && go test ./... && go vet ./...`
Expected: All pass, no references to removed files

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "chore: remove old HTMX search results template and obsolete handlers"
```

---

## Task 10: Integration Testing and Final Verification

**Files:**
- Modify: `internal/web/handlers_drive_test.go` (add integration-style test)

- [ ] **Step 1: Write end-to-end flow test**

Add to `internal/web/handlers_drive_test.go`:

```go
func TestDriveSelectFlow_PersistsAcrossRefresh(t *testing.T) {
	mgr := drivemanager.NewManager(&driveWithDiscExecutor{discName: "Seinfeld Season 1"}, nil)
	mgr.PollOnce(context.Background())

	cfg := config.AppConfig{OutputDir: "/tmp/test"}
	srv := &Server{
		echo:          echo.New(),
		cfg:           &cfg,
		driveMgr:      mgr,
		sseHub:        NewSSEHub(),
		driveSessions: NewDriveSessionStore(),
	}
	srv.echo.POST("/drives/:id/select", srv.handleDriveSelect)
	srv.echo.GET("/drives/:id", srv.handleDriveDetail)

	// Step 1: Select a release.
	selectBody := `{"mediaItemID":"1","releaseID":"10","title":"Seinfeld","year":"1989","type":"Series"}`
	selectReq := httptest.NewRequest(http.MethodPost, "/drives/0/select", strings.NewReader(selectBody))
	selectReq.Header.Set("Content-Type", "application/json")
	selectReq.Header.Set("Accept", "application/json")
	selectRec := httptest.NewRecorder()
	srv.echo.ServeHTTP(selectRec, selectReq)

	if selectRec.Code != http.StatusOK {
		t.Fatalf("select: expected 200, got %d", selectRec.Code)
	}

	// Step 2: "Refresh" — GET the drive detail page.
	detailReq := httptest.NewRequest(http.MethodGet, "/drives/0", nil)
	detailRec := httptest.NewRecorder()
	srv.echo.ServeHTTP(detailRec, detailReq)

	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail: expected 200, got %d", detailRec.Code)
	}

	body := detailRec.Body.String()

	// The store JSON should contain the persisted selection.
	if !strings.Contains(body, "Seinfeld") {
		t.Error("detail page should contain 'Seinfeld' in store JSON after select")
	}
	if !strings.Contains(body, `"mediaItemID":"1"`) || !strings.Contains(body, `"releaseID":"10"`) {
		t.Error("detail page should contain selected release IDs in store JSON")
	}
}

func TestDriveSessionClearedOnEject(t *testing.T) {
	store := NewDriveSessionStore()
	store.Set(0, &DriveSession{
		MediaTitle: "Seinfeld",
		ReleaseID:  "10",
	})

	// Simulate eject by clearing session.
	store.Clear(0)

	if session := store.Get(0); session != nil {
		t.Error("expected session to be nil after clear (simulating eject)")
	}
}
```

- [ ] **Step 2: Run the new tests**

Run: `go test ./internal/web/ -run "TestDriveSelectFlow_PersistsAcrossRefresh|TestDriveSessionClearedOnEject" -v`
Expected: PASS

- [ ] **Step 3: Run full test suite with race detector**

Run: `go test ./... -race`
Expected: All pass, no races

- [ ] **Step 4: Build Docker image**

Run: `go build -o /dev/null .`
Expected: Clean build

- [ ] **Step 5: Commit**

```bash
git add internal/web/handlers_drive_test.go
git commit -m "test: add integration tests for Alpine.js select-refresh flow"
```

- [ ] **Step 6: Final commit with CLAUDE.md update**

```bash
git add CLAUDE.md
git commit -m "docs: document Alpine.js + SSE design pattern in CLAUDE.md"
```
