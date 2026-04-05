# Unified Rip Activity Page Design

**Date:** 2026-04-02

## Context

The disc detail page (Step 5) and the `/activity` page both show rip progress, creating redundancy. Step 5 shows a minimal per-drive job list after submission; `/activity` shows a fuller view with cancel/remove controls but minimal metadata. Neither page shows rich track metadata (file size, duration, audio tracks, subtitles) that is available at scan time.

The goal is to merge these into a single canonical `/activity` page with rich job metadata, remove the rip-monitoring UI from Step 5, and redirect there after a rip is submitted.

---

## Data Model

### New: `TrackMetadata` struct (in `ripper` package)

```go
type AudioTrack struct {
    Language string // e.g. "English"
    Codec    string // e.g. "TrueHD"
    Channels string // e.g. "7.1"
}

type TrackMetadata struct {
    SizeBytes         int64
    SizeHuman         string       // e.g. "42.3 GB"
    Duration          string       // e.g. "2:14:33"
    AudioTracks       []AudioTrack
    SubtitleLanguages []string     // e.g. ["English", "French"]
}
```

### `ripper.Job` changes
- Add `TrackMetadata TrackMetadata` field
- Include in JSON serialization (for SSE broadcasts)
- Populated at job submission from `makemkv.TitleInfo`

### DB migration
- `ALTER TABLE rip_jobs ADD COLUMN track_metadata TEXT DEFAULT NULL`
- Stores `TrackMetadata` serialized as JSON
- Populated at job insertion time in the workflow

### `db.RipJob` struct
- Add `TrackMetadata string` field (raw JSON)
- Deserialize to `TrackMetadata` struct when building JSON responses

---

## Step 5 → Redirect Flow

### Success path
1. User clicks "Start Rip" on the disc detail page (Step 5)
2. Handler submits the job successfully
3. Handler returns `HX-Redirect` header pointing to `/activity?flash=Rip+started+successfully`
4. Browser navigates to `/activity`; the flash banner is shown and then auto-dismissed

### Error path
1. Handler encounters an immediate error (e.g. job submission fails)
2. No redirect; handler returns inline error HTML rendered within the Step 5 UI
3. User remains on the disc detail page and can retry or adjust settings

### Step 5 UI cleanup
- The rip-in-progress job list (currently rendered in Step 5 after submission) is **removed** from `drive_detail.templ`
- Step 5 retains only the submission form, options, and error display area
- The `ripJobs` Alpine store and its hydration on the drive detail page are **removed entirely** (no longer needed)

---

## Unified `/activity` Page

### Flash banner
- Shown at top of page (above tabs) when `?flash=` query param is present
- Dismissible green alert managed by Alpine.js
- Alpine removes the param from the URL after display via `history.replaceState` to prevent re-show on refresh

### Tab structure (preserved)
- Tab 1: **Active & Queued**
- Tab 2: **History**

### Active & Queued tab — card layout
Each job is a card (not a flat table row) showing:
- Title name + disc name
- Content type badge (Movie / TV / Extra / etc.)
- File size + duration (e.g. "42.3 GB · 2:14:33")
- Audio tracks summary (e.g. "TrueHD 7.1 EN, AC3 5.1 FR")
- Subtitle languages (e.g. "EN, FR, ES")
- Progress bar (active jobs) or queue position indicator (pending jobs)
- Cancel button

### History tab — card layout
Same card layout as Active & Queued, with these additional fields:
- Date completed
- Status badge (Completed / Failed / Skipped)
- Output path (for completed jobs)
- Error message (for failed jobs)
- Total duration of the rip

Existing pagination (50 per page) is preserved.

### SSE updates
- `TrackMetadata` fields ride along in existing `rip-update` SSE events automatically (no hub/wiring changes needed)
- Alpine store update logic extended to include new fields
- Cards re-render reactively as jobs transition states

---

## Handler Changes

### `handlers_activity.go`
- Read `?flash=` query param and pass to template
- `RipJobJSON` (in `json_helpers.go`) extended with `TrackMetadata` fields for initial page-load store hydration
- DB query for history deserializes `track_metadata` JSON column and includes it in response

### `handlers_drive.go`
- On successful rip submission: return `HX-Redirect` to `/activity?flash=...`
- On error: return inline error HTML for Step 5 (no redirect)
- Remove `ripJobs` Alpine store hydration from the drive detail page entirely

---

## Key Files

| File | Change |
|------|--------|
| `internal/ripper/job.go` | Add `TrackMetadata` / `AudioTrack` structs; add field to `Job` |
| `internal/workflow/workflow.go` | Populate `TrackMetadata` from `TitleInfo` at job submission |
| `internal/db/db.go` | Add `TrackMetadata` field to `RipJob`; update insert/query |
| `migrations/` | New migration: add `track_metadata TEXT` column to `rip_jobs` |
| `internal/web/json_helpers.go` | Extend `RipJobJSON` with metadata fields |
| `internal/web/handlers_activity.go` | Flash param, richer DB query, updated JSON shape |
| `internal/web/handlers_drive.go` | HX-Redirect on success, inline error on failure |
| `templates/activity.templ` | Card layout for both tabs, flash banner |
| `templates/drive_detail.templ` | Remove rip-in-progress job list from Step 5 |

---

## Verification

1. Submit a rip from the disc detail page → browser redirects to `/activity` with green flash banner
2. Immediate submission error → stays on disc detail page, error shown in Step 5
3. Active & Queued tab shows file size, duration, audio tracks, subtitles on each card
4. Cancel button removes job from queue / cancels active rip
5. After rip completes, job appears in History tab with full metadata
6. Reload `/activity` (no `?flash=`) → no banner shown
7. Run `go test ./...` — all existing tests pass
