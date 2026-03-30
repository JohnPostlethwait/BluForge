# Alpine.js Frontend Migration Design

**Date:** 2026-03-29
**Status:** Draft
**Scope:** Dashboard and Drive Detail pages

## Problem

BluForge's pure HTMX frontend struggles with multi-step stateful workflows. The drive detail page's search-select-scan-rip flow has produced repeated bugs: lost state across refreshes, dead-end UI states, polling that doesn't update, and query-param juggling to preserve selections. These are symptoms of managing workflow state across stateless HTTP round-trips.

## Solution

Add Alpine.js alongside HTMX. Alpine manages client-side reactive state and renders dynamic UI. HTMX handles form submissions and page navigation. SSE delivers JSON data that updates Alpine stores, replacing HTML partial swaps and polling.

## Design Pattern (project-wide)

All Alpine-enabled pages follow this pattern:

```
SSE delivers JSON -> Alpine.store() updates -> Alpine templates re-render
HTMX handles form POSTs and page navigation
Accept header determines response format (JSON vs HTML)
```

This pattern must be documented in CLAUDE.md.

## Content Negotiation

All endpoints consumed by Alpine use HTTP `Accept` header content negotiation:

- Alpine/JS fetch calls set `Accept: application/json` — server returns JSON
- HTMX form submissions send `Accept: text/html` (default) — server returns HTML
- Server checks `c.Request().Header.Get("Accept")`: if it contains `application/json`, return JSON; otherwise return HTML

This keeps a single endpoint per resource and makes the expected format self-documenting.

## Architecture

### Client-Side Stack

- **Alpine.js** loaded from CDN in `layout.templ` (after HTMX)
- **HTMX SSE extension removed** from Alpine-enabled pages — Alpine manages `EventSource` directly
- **`Alpine.store()`** holds shared reactive state, hydrated from server-rendered JSON on page load
- **HTMX** retained for form POSTs (`hx-post`) and page navigation (`<a href>`)

### Server-Side Additions

**`DriveSession` map on Server struct:**
- Type: `map[int]*DriveSession` protected by `sync.RWMutex`
- `DriveSession` struct fields: `MediaItemID`, `ReleaseID`, `MediaTitle`, `MediaYear`, `MediaType`, `SearchResults`
- Populated by `POST /drives/:id/select`
- Cleared on disc eject events (in the existing drive event callback in `main.go`)
- Read by `handleDriveDetail` to hydrate Alpine store on page load

## Pages In Scope

### Dashboard

**Alpine store:** `Alpine.store('drives')`

```js
{
  ready: false,          // true after first drive poll
  list: [                // all detected drives
    { index: 0, name: "BD-RE ASUS...", discName: "Seinfeld Season 1", state: "detected" }
  ]
}
```

**Hydration:** Server renders `<script>Alpine.store('drives', { ... })</script>` with current drive state.

**SSE:** Alpine initializes an `EventSource('/events')` on page load. `drive-update` events carry the full drive list as JSON. Alpine store updates reactively, re-rendering drive cards via `<template x-for="drive in $store.drives.list">`.

**Removed:** `GET /drives-partial` endpoint, `hx-trigger="every 2s"` polling, HTMX SSE extension attributes.

### Drive Detail

**Alpine store:** `Alpine.store('drive')`

```js
{
  driveIndex: 0,
  driveName: "BD-RE ASUS...",
  discName: "Seinfeld Season 1",
  state: "detected",
  scanning: false,
  titles: [
    { index: 0, name: "...", duration: "01:23:45", size: "32.1 GB", sourceFile: "00001.mpls", selected: true }
  ],
  selectedRelease: null,  // or { mediaItemID, releaseID, title, year, type }
  searchResults: [],
  ripProgress: null
}
```

**Hydration:** Server reads `driveSession[driveIndex]` and renders the full store state into a `<script>` tag. Selected release and search results survive browser refresh.

**SSE events consumed:**
- `scan-complete` (new): `{ driveIndex, titles: [...] }` — populates title table, sets `scanning: false`
- `drive-event`: `{ type, driveIndex, discName, state }` — updates disc info, clears state on eject
- `rip-update`: `{ driveIndex, progress, status }` — updates rip progress display

## Data Flow

### Search Flow

1. User types query, Alpine `@submit.prevent` handler fires a `fetch()` POST to `/drives/:id/search` with `Accept: application/json` and the query as form data
2. Server returns JSON array of search results
3. Alpine parses response, sets `$store.drive.searchResults`
4. Alpine re-renders results table via `<template x-for>`

Note: The search form uses Alpine's `@submit.prevent` + `fetch()` instead of HTMX `hx-post`, because Alpine needs to control the `Accept` header and handle the JSON response directly. HTMX is not used for requests that return JSON to Alpine.

### Select Flow

1. User clicks "Select" — Alpine `@click` handler, no page reload
2. Alpine updates `$store.drive.selectedRelease` immediately (instant UI update)
3. Alpine fires `POST /drives/:id/select` with `Accept: application/json` to persist selection server-side
4. Server stores in `driveSession`, triggers background scan if no cached scan exists
5. Server responds with `{ scanning: true/false, titles: [...] }`
6. Alpine updates `scanning` and `titles` from response
7. If scanning, `scan-complete` SSE event will deliver titles when ready

### Rip Flow

1. Alpine builds form data from store: selected title indices + release metadata
2. HTMX POST to `/drives/:id/rip` (standard form submission)
3. Server creates rip jobs, redirects to `/queue`

### Page Refresh

1. Browser refreshes `/drives/:id`
2. Server reads `driveSession[driveIndex]` for persisted selection
3. Server reads cached scan for titles
4. Renders page with `<script>Alpine.store('drive', { ...fullState })</script>`
5. Alpine hydrates, UI renders with all state intact

## Endpoint Changes

| Endpoint | Before | After |
|----------|--------|-------|
| `GET /drives/:id` | HTML page with query param state | HTML page with JSON store in `<script>` tag |
| `POST /drives/:id/search` | Returns HTML partial | Returns JSON (when `Accept: application/json`) or HTML |
| `POST /drives/:id/select` | Did not exist | New — persists selection, triggers scan, returns JSON |
| `GET /drives-partial` | HTML partial for dashboard polling | Removed |
| `GET /events` (SSE) | HTML-oriented events | JSON events for Alpine-enabled pages |

### New SSE Event

**`scan-complete`:**
```json
{
  "driveIndex": 0,
  "titles": [
    { "index": 0, "name": "...", "duration": "01:23:45", "size": "32.1 GB", "sourceFile": "00001.mpls" }
  ]
}
```

Published when `ScanDisc` completes in the orchestrator. Eliminates polling for scan results.

## Template Changes

### layout.templ
- Add Alpine.js CDN script tag after HTMX
- Keep HTMX SSE extension for non-Alpine pages (queue)

### dashboard.templ
- Remove `DriveGrid` partial component
- Add `x-data` with `EventSource` initialization
- Drive cards: `<template x-for="drive in $store.drives.list">` with `x-bind` for dynamic attributes
- Loading state: `x-show="!$store.drives.ready"`
- Empty state: `x-show="$store.drives.ready && $store.drives.list.length === 0"`

### drive_detail.templ
- Add `x-data` with `EventSource` initialization
- Search results: `<template x-for="r in $store.drive.searchResults">`
- Select button: `@click` sets `$store.drive.selectedRelease` and fires persist POST
- Matched banner: `x-show="$store.drive.selectedRelease"`
- Scanning state: `x-show="$store.drive.scanning"`
- Titles table: `<template x-for="t in $store.drive.titles">`
- Rip form: `x-bind:value` on hidden fields from `$store.drive.selectedRelease`

### drive_search_results.templ
- Removed entirely. Search results rendered client-side by Alpine in `drive_detail.templ`.

### components/drive_card.templ
- Removed as a server-rendered Go component. Drive card HTML moves inline into `dashboard.templ` as an Alpine template fragment.

### Unchanged
- `queue.templ` — keeps existing HTMX SSE pattern
- `settings.templ` — pure form
- `history.templ` — pagination
- `contribute.templ` — static

## What Gets Removed

- `GET /drives-partial` endpoint and `handleDrivesPartial` handler
- `drive_search_results.templ` and `drive_search_results_templ.go`
- `components/drive_card.templ` and `drive_card_templ.go` (inlined into dashboard)
- Query param state passing on drive detail refresh URLs (`media_item_id`, `release_id`, etc.)
- Hidden form fields for release metadata on drive detail page
- `hx-trigger="every 2s"` polling on dashboard and drive detail
- `hx-select=".card:last-child"` partial extraction hack
- `hx-vals` JSON on search result Select button

## Testing

- **Existing Go tests:** Update `TestHandleDriveSearch_SelectFlow` to expect JSON response when `Accept: application/json` is set. Update `TestHandleDrivesPartial` — remove or replace with test for JSON drive list.
- **New tests:** `TestHandleDriveSelect` for the new `/drives/:id/select` endpoint (persists session, triggers scan, returns JSON). `TestDriveSessionClearedOnEject` to verify eject event clears the session.
- **`TestMediaItemsToRows`:** Unchanged, still validates the flattening logic.
- **Manual testing:** Verify on Unraid deployment: search → select → scan → rip flow with browser refresh at each step.

## Trade-offs

### What gets better
- No more lost state — selected release lives in Alpine store + server session
- No more polling — SSE pushes updates directly into Alpine stores
- Instant Select — Alpine reactivity, no round-trip for UI update
- Simpler templates — no query param URLs, no hidden fields, no partial extraction hacks
- Scan completion is an event, not a polling discovery

### What gets more complex
- Two rendering models: Alpine templates (client-side) alongside Go templ (server-rendered)
- Content negotiation: endpoints check `Accept` header to determine response format
- Alpine.js as a CDN dependency (same risk as HTMX)
- Queue page uses different pattern (HTMX SSE) than dashboard/drive detail (Alpine SSE)
