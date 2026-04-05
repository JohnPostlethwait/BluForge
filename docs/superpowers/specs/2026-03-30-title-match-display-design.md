# Per-Title DiscDB Match Display

## Goal

After a user has both scanned a disc and selected a DiscDB release, automatically show which scanned title maps to which episode/content in the Titles table. Auto-select only matched titles. Provide select-all / deselect-all controls.

## Architecture

The matching logic already exists in `internal/discdb/matcher.go` via `MatchTitles()`. This feature surfaces that data to the UI by enriching the existing `TitleJSON` response and triggering matching automatically when both scan results and a selected release are available.

## Data Flow

Two user orderings are supported:

1. **Scan first, then select release** — match triggered in the select handler
2. **Select release first, then scan** — match triggered in the scan handler

Both paths produce the same enriched title JSON.

### Trigger Point 1: Select Release (`handleDriveSelectAlpine`)

After saving the release selection to `DriveSession`, check if a scan is cached for this drive (via `orchestrator.GetCachedScan(driveIndex)`). If so, fetch the DiscDB disc for the selected release, run `MatchTitles(scan, disc)`, and include enriched titles in the JSON response.

### Trigger Point 2: Scan Disc (`handleDriveScan`)

After scanning, check if a release is selected in `DriveSession`. If so, fetch the DiscDB disc for the selected release, run `MatchTitles(scan, disc)`, and return enriched titles instead of plain titles.

### Shared Match Logic

Both trigger points call the same function to build enriched titles. This function:

1. Receives a `*makemkv.DiscScan` and a `discdb.Disc`
2. Calls `discdb.MatchTitles(scan, disc)` to get `[]ContentMatch`
3. Builds `[]TitleJSON` with match fields populated
4. Sets `Selected = true` only for matched titles

## Backend Changes

### New endpoint: `POST /drives/:id/match`

Standalone endpoint that runs matching when both scan + release exist. Returns enriched `[]TitleJSON`. This serves as a fallback if the inline trigger points miss (e.g., page refresh with both cached).

### Enriched `TitleJSON`

Add optional fields to the existing `TitleJSON` struct in `json_helpers.go`:

```go
type TitleJSON struct {
    Index        int    `json:"index"`
    Name         string `json:"name"`
    Duration     string `json:"duration"`
    Size         string `json:"size"`
    SourceFile   string `json:"sourceFile"`
    Selected     bool   `json:"selected"`
    Matched      bool   `json:"matched"`
    ContentTitle string `json:"contentTitle,omitempty"`
    ContentType  string `json:"contentType,omitempty"`
    Season       string `json:"season,omitempty"`
    Episode      string `json:"episode,omitempty"`
}
```

### Orchestrator: expose cached scan

Add `GetCachedScan(driveIndex int) *DiscScan` to the orchestrator (or the relevant interface) so the web handlers can access the cached scan without re-running makemkvcon.

### DiscDB: fetch disc by release ID

The select handler stores `releaseID` and `mediaItemID`. To run `MatchTitles`, we need the `discdb.Disc` object. Currently `DriveSession.SearchResults` stores flattened `[]SearchResultJSON` which lacks disc title data. Add a `RawSearchResults []discdb.MediaItem` field to `DriveSession` to cache the full API response alongside the flattened JSON. When matching, look up the disc from `RawSearchResults` by matching `releaseID` and selecting the first disc (single-disc releases) or matching by disc index. No new API call needed.

## Frontend Changes

### Titles table columns

When match data is present (at least one title has `matched: true`), display:

```
[ ] | # | Match              | Name             | Duration | Size
[x] | 0 | S01E01 - The Se... | Seinfeld Season 1 | 0:23:01  | 10.9 GB
[ ] | 6 | Unmatched          | Seinfeld Season 1 | 0:02:17  | 312.6 MB
```

- **Match column**: For matched titles, show formatted content info. For series: `S{season}E{episode} - {contentTitle}`. For movies: `{contentTitle}`. For unmatched: "Unmatched" in muted styling.
- Unmatched titles have their checkbox unchecked by default.

When no match data is present (no release selected), the Match column is hidden and the table looks as it does today.

### Select All / Deselect All

Add controls in the Titles card header (next to "Scan Disc" button):

- "Select All" link/button — checks all title checkboxes
- "Deselect All" link/button — unchecks all title checkboxes

These are simple Alpine click handlers that iterate `$store.drive.titles` and toggle `selected`.

### JS changes

- `selectRelease()`: If the response includes a `titles` array, update `$store.drive.titles` with the enriched data.
- `scanDisc()`: No change needed — the response already populates `$store.drive.titles`, and will now include match fields when a release is selected.
- No additional fetch calls needed. Both trigger points return data inline.

### Alpine store hydration

On page load (`handleDriveDetail`), if both a cached scan and a selected release exist in session, hydrate the store with enriched titles so the match data survives page refreshes.

## Files Modified

| File | Change |
|------|--------|
| `internal/web/json_helpers.go` | Add match fields to `TitleJSON`, add enriched title builder function |
| `internal/web/handlers_drive.go` | Enrich `handleDriveScan` response when release selected; add `handleDriveMatch` endpoint |
| `internal/web/handlers_drive_select.go` | Return enriched titles in select response when scan cached |
| `internal/web/drive_session.go` | Add `RawSearchResults []discdb.MediaItem` field for disc lookup during matching |
| `internal/workflow/orchestrator.go` | Expose `GetCachedScan()` method |
| `internal/web/server.go` | Register `POST /drives/:id/match` route |
| `templates/drive_detail.templ` | Add Match column, select/deselect all controls, conditional column visibility |

## Not In Scope

- Manual editing of match results
- Match confidence scores or alternate match suggestions
- Changes to the rip form submission (already sends title indices + release metadata)
- Changes to `MatchTitles()` logic itself
