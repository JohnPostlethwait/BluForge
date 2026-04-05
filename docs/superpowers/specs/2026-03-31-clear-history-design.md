# Clear History Log — Design Spec

**Date:** 2026-03-31

## Context

The Activity page's History tab accumulates all past rip job records in the SQLite database. During development and debugging, many test entries build up with no way to clear them from the UI. This feature adds a "Clear Log" button to the History tab that deletes all past rip job records, excluding any jobs currently active or queued in the rip engine.

## Scope

A single "Clear Log" button on the History tab that wipes all `rip_jobs` DB rows not currently active or queued.

---

## Architecture

### 1. Database — `internal/db/jobs.go`

Add `DeleteJobsExcept(excludeIDs []int64) error`:

- If `excludeIDs` is non-empty: `DELETE FROM rip_jobs WHERE id NOT IN (...)`
- If `excludeIDs` is empty: `DELETE FROM rip_jobs`

### 2. HTTP Handler — `internal/web/handlers_activity.go`

Add `handleActivityClearHistory(c echo.Context) error`:

1. Collect job IDs from rip engine's active and queued sets
2. Call `db.DeleteJobsExcept(excludeIDs)`
3. Return `{"status": "ok"}` JSON

### 3. Route — `internal/web/server.go`

Register: `POST /activity/clear-history`

### 4. Template — `templates/activity.templ`

Add "Clear Log" button in the History tab header (right-aligned, alongside any existing controls). On click:

- `fetch` POST to `/activity/clear-history` with `Accept: application/json`
- On success: set `$store.activity.history = []` to immediately empty the table in the Alpine store

---

## Data Flow

```
[Clear Log button click]
  → fetch POST /activity/clear-history
  → handler: get active+queued IDs from engine
  → db.DeleteJobsExcept(ids)
  → return {"status": "ok"}
  → Alpine: $store.activity.history = []
  → History table re-renders empty
```

---

## Error Handling

- DB error returns HTTP 500 with `{"error": "..."}` — button shows no feedback beyond a console log (not worth a UI error state for a debug utility)

---

## Verification

1. Run the app: `go build -o bluforge . && ./bluforge`
2. Navigate to Activity → History tab — confirm existing entries are visible
3. Click "Clear Log" — table should empty immediately
4. Refresh the page — history should remain empty
5. With an active rip in progress: click "Clear Log" — the active job's DB record should survive; completed entries should be gone
6. Run `go test ./internal/db/ -run TestDeleteJobsExcept` (new unit test)
