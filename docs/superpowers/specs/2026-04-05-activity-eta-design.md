# Activity Page: Elapsed Time & ETA Display

**Date:** 2026-04-05  
**Branch:** `feature/activity-eta`

## Context

The `/activity` page shows active rips with a progress bar and percentage, but gives no indication of how long the rip has been running or how long it will take. Users have no way to plan around completion. `Job.StartedAt` is already tracked server-side and the full Job struct is already broadcast over SSE — the data just isn't exposed to the frontend.

## Goal

Show elapsed time and estimated time remaining on each active rip card, below the progress bar. Show only elapsed until progress reaches 5%, then reveal the remaining estimate once it's meaningful.

**Display format:** `12m elapsed · ~14m remaining`

## Approach

Pure frontend calculation. Expose `StartedAt` from the server in the initial page payload and extract it from SSE updates. A 1-second Alpine reactive clock ticker drives live updates without extra SSE traffic.

## Backend Change

**File:** `internal/web/handlers_activity.go`

Add `StartedAt` to `activityJobJSON`:

```go
type activityJobJSON struct {
    // ... existing fields ...
    StartedAt string `json:"startedAt,omitempty"`
}
```

Populate it for active jobs only (not pending, completed, or history):

```go
StartedAt: j.StartedAt.UTC().Format(time.RFC3339),
```

`Job.StartedAt` is set by `job.Start()` in `internal/ripper/job.go` when ripping begins, so it is always populated for active jobs. No DB schema changes needed.

## Frontend Changes

**File:** `templates/activity.templ`

### 1. Store ticker

After the existing `Alpine.store('activity', ...)` initialization in the `alpine:init` block, add:

```js
Alpine.store('activity').now = Date.now()
setInterval(() => Alpine.store('activity').now = Date.now(), 1000)
```

This is the standard Alpine pattern for reactive live clocks. All timing displays derive from this single `now` value.

### 2. SSE handler

In the `rip-update` event handler, alongside the existing `progress` and `status` extractions, add:

```js
active.startedAt = job.StartedAt
```

### 3. `formatDuration` helper

Add a JS helper in the script block:

```js
function formatDuration(secs) {
    if (secs < 60) return '<1m'
    const h = Math.floor(secs / 3600)
    const m = Math.floor((secs % 3600) / 60)
    return h > 0 ? h + 'h ' + m + 'm' : m + 'm'
}
```

### 4. Timing display row

Below the existing `.progress-row` in the active job card template, add:

```html
<div class="text-muted text-sm mt-1" x-show="j.startedAt">
  <span x-text="j.startedAt ? (() => {
    const elapsed = Math.floor(($store.activity.now - new Date(j.startedAt)) / 1000)
    const elapsedStr = formatDuration(elapsed)
    if (j.progress >= 5) {
      const total = elapsed / (j.progress / 100)
      const remaining = Math.max(0, total - elapsed)
      return elapsedStr + ' elapsed · ~' + formatDuration(remaining) + ' remaining'
    }
    return elapsedStr + ' elapsed'
  })() : ''"></span>
</div>
```

## Files Changed

| File | Change |
|------|--------|
| `internal/web/handlers_activity.go` | Add `StartedAt string` to `activityJobJSON`; populate for active jobs |
| `templates/activity.templ` | Store ticker, SSE extraction, `formatDuration` helper, timing display row |

## Verification

1. Start the app with a disc inserted and a rip in progress.
2. Navigate to `/activity` — the active job card should show elapsed time immediately.
3. Once progress reaches 5%, a `· ~Xm remaining` suffix should appear.
4. The display should update every second without page reload.
5. On job completion, the timing row disappears with the active card (it moves to completed).
6. Run `go test ./...` — all tests pass (no Go logic changes beyond the new struct field).
7. Run `templ generate` — no errors.
