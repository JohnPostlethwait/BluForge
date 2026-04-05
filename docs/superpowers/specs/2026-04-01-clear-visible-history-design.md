# Clear Visible History — Design Spec

**Date:** 2026-04-01

## Context

The Activity page History tab has a "Clear Log" button that deletes all rip job history from the database (excluding currently active/queued jobs). Users requested a "Clear Visible" button that deletes only the jobs currently matched by the active filters (text search and/or status dropdown), while retaining unmatched records. The existing button is renamed "Clear All" for clarity.

## Requirements

- **Clear All** (renamed from "Clear Log"): unchanged behavior — deletes all history except active/queued jobs.
- **Clear Visible**: deletes only jobs that match the current `filter` (text search on disc name or title name) and `statusFilter` (status dropdown) simultaneously, still excluding active/queued jobs.
- "Clear Visible" matches across the full database — not just the currently loaded page.
- Active/queued jobs are always protected, even if they match the filters.
- "Clear Visible" is disabled when no filters are active (`filter === ''` and `statusFilter === 'all'`).

## Database Layer

**File:** `internal/db/jobs.go`

Add:

```go
DeleteJobsByFilter(search, status string, excludeIDs []int64) error
```

- If `status` is non-empty and not `"all"`: `AND status = ?`
- If `search` is non-empty: `AND (disc_name LIKE ? OR title_name LIKE ?)` with `%search%` wildcards
- If `excludeIDs` is non-empty: `AND id NOT IN (...)`
- Uses parameterized queries throughout.

## Backend Handler

**File:** `internal/web/handlers_activity.go`

Add `handleActivityClearFiltered`:

1. Decode JSON body: `{ "search": string, "status": string }`
2. Collect active/queued job IDs from the rip engine (same pattern as `handleActivityClearHistory`)
3. Call `store.DeleteJobsByFilter(search, status, excludeIDs)`
4. Return `{"status": "ok"}` on success, HTTP 500 on error

**Route:** `POST /activity/clear-filtered` registered alongside the existing `POST /activity/clear-history`.

## Frontend

**File:** `templates/activity.templ`

1. Rename "Clear Log" button label to **"Clear All"** — no logic change.
2. Add **"Clear Visible"** button next to "Clear All":
   - `btn-danger` styling (destructive action)
   - Disabled when `filter === '' && statusFilter === 'all'`
3. Add `clearFiltered(btn, search, status)` Alpine function:
   - Called as `@click="clearFiltered($el, filter, statusFilter)"` so the `x-data` scope values are passed as arguments
   - POSTs `{ search, status }` as JSON to `/activity/clear-filtered`
   - On success: removes matching entries from `Alpine.store('activity').history` client-side using the same filter predicate (avoids full reload)
   - On error: shows alert, re-enables button

## Verification

- With no filters active: "Clear Visible" button is disabled.
- With status filter set to "failed": clicking "Clear Visible" removes only failed jobs; completed jobs remain.
- With text search "Batman": clicking "Clear Visible" removes only jobs whose disc or title name contains "Batman".
- With both filters active: only jobs matching both criteria are deleted.
- An active/queued job that matches the filters is not deleted.
- "Clear All" behavior is unchanged.
- Run `go test ./internal/db/ ./internal/web/` to confirm unit tests pass.
