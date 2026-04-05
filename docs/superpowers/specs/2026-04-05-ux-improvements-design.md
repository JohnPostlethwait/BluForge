# UX Improvements: Track Selection, DiscDB Identifiers, Activity Track Filtering

**Date:** 2026-04-05  
**Status:** Approved

## Overview

Three related UX fixes for the drive detail workflow and activity page:

1. **Smart default title selection** — after a DiscDB match, pre-select only titles with identified content; leave stubs unchecked.
2. **DiscDB identifiers in the UI** — show UPC, ASIN, and region in the search results table and in all post-selection confirmation banners.
3. **Activity page track filtering** — the active/queued/history cards should show only the tracks being ripped, not all tracks on the disc.

---

## Issue 1: Smart Default Title Selection

### Root Cause

`discdb.MatchTitles` sets `cm.Matched = true` for any scan title whose source file appears anywhere in the DiscDB disc entry — **regardless of whether that entry has assigned content** (`dt.Item == nil`). For a disc like TRON: Legacy 4K (163 raw titles, 162 unidentified stubs, 1 main movie), all 163 scan titles return `Matched=true`, so `enrichTitlesWithMatches` pre-selects all of them. The net result is identical to scanning without any DiscDB selection.

### Fix

**`internal/discdb/matcher.go` — `MatchTitles`:**  
Add `dt.Item != nil` guard. A title is only "matched" when DiscDB has assigned content to it.

```go
if dt, ok := lookup[sf]; ok && dt.Item != nil {
    cm.Matched = true
    // populate ContentType, ContentTitle, Season, Episode from dt and dt.Item
}
```

Titles whose source file is in DiscDB but has no item assignment become `Matched=false` — they show as "Unmatched" in the review table, and are not pre-selected.

**`internal/web/json_helpers.go` — `enrichTitlesWithMatches`:**  
Add a fallback for fully-stub discs (where the selected disc has zero identified titles). Without this, a stub disc leaves the user on step 4 with nothing checked and no explanation.

```
hasAnyIdentified := any match with Matched=true

for each title:
    if Matched → Selected=true
    else if !hasAnyIdentified → Selected=true  (fallback: stub disc, preserve current behavior)
    else → Selected=false
```

### Outcomes

| Disc | Before | After |
|------|--------|-------|
| TRON: Legacy 4K (1 identified / 163 total) | 163 selected | 1 selected |
| TRON: Legacy Blu-ray (8 identified / 62 total) | 62 selected | 8 selected |
| Fully-stub disc (0 identified) | N selected | N selected (fallback) |
| No DiscDB selection | N selected | N selected (unchanged) |

---

## Issue 2: DiscDB Identifiers in the UI

### Data Flow

`SearchResultJSON` already carries `releaseUPC`, `releaseASIN`, and `regionCode` from the API response. These need to be:

1. Surfaced in the step 2 search results table (pre-selection).
2. Stored in `SelectedReleaseJSON` and `DriveSession` when a release is selected.
3. Displayed in the step 3 and step 4 confirmation banners (post-selection).

### Changes

**`internal/web/json_helpers.go` — `SelectedReleaseJSON`:**  
Add fields: `ASIN string`, `RegionCode string`, `Locale string`.

**`internal/web/drive_session.go` — `DriveSession`:**  
Add fields: `ReleaseASIN string`, `ReleaseRegionCode string`, `ReleaseLocale string`.

**`internal/web/handlers_drive_select.go` — `selectRequest` and `handleDriveSelectAlpine`:**  
Accept and persist the new fields from the client request.

**`templates/drive_detail.templ` — Alpine `doSelectRelease`:**  
Include `asin`, `regionCode`, `locale` in the POST body (sourced from the search result object `r`).

**Step 2 search results table:**  
Add ASIN and Region columns after UPC:

```
Title | Year | Type | Release | UPC | ASIN | Region | Format | [Select]
```

Both columns show `—` when empty (many DiscDB entries lack ASIN or region).

**Step 3 confirmation banner** (currently: `Matched: Title (Year) — Type`):

```
Matched: TRON: Legacy (2010) — Movie
UPC: 043396643703 · ASIN: B0FRNKMGW6 · Region 1
```

The second line is conditionally rendered only when at least one of UPC or ASIN is non-empty.

**Step 4 release reminder** (currently: `Organizing as: Title (Year)`):

```
Organizing as: TRON: Legacy (2010) — Movie
UPC: 043396643703 · ASIN: B0FRNKMGW6 · Region 1 · en-us
```

Same conditional rendering rule. Locale is included on step 4 for full regional disambiguation.

---

## Issue 3: Activity Page Track Filtering

### Root Cause

`buildTrackMetadata` in `internal/workflow/orchestrator.go` collects **all** audio and subtitle streams from the scanned title unconditionally. The `SelectionOpts` (language filters, lossless preference) are passed to the MakeMKV ripper separately and never applied to the stored metadata. As a result, the activity page shows every track on the disc rather than what was actually ripped.

### Fix

**`internal/workflow/orchestrator.go` — `buildTrackMetadata`:**  
Add an `opts *makemkv.SelectionOpts` parameter. Apply filtering while iterating streams:

- **Audio tracks:** skip if `opts.AudioLangs` is non-empty and `s.LangCode()` is not in the list; skip lossless codecs (`TrueHD`, `DTS-HD MA`, `FLAC`, `PCM`) if `!opts.KeepLossless`.
- **Subtitle languages:** skip if `opts.SubtitleLangs` is non-empty and `s.LangCode()` is not in the list.
- **nil opts:** no filtering (preserves current behavior for auto-rip paths that have no language filter).

**Call sites in `orchestrator.go`:**

| Call site | opts to pass |
|-----------|-------------|
| Manual rip title building | `params.SelectionOpts` |
| Auto-rip title building | `autoRipConfig.SelectionOpts` |
| Title building from saved mapping (no opts context) | `nil` |

### Outcome

For TRON: Legacy ripped with English audio + English subtitles only:

- **Before:** `TrueHD English, AC3 English, DTS-HD MA French, AC3 French, AC3 Spanish, DTS Spanish, DTS-HD MA German… · Subs: English, French, Spanish, German…`
- **After:** `TrueHD English, AC3 English · Subs: English`

The filtered metadata is stored in `rip_jobs.track_metadata` and used for all display contexts (active, queued, completed, history). No other systems (scan cache, DiscDB cache, disc mappings) are affected — they operate on separate data structures.

---

## Files Touched

| File | Change |
|------|--------|
| `internal/discdb/matcher.go` | Add `dt.Item != nil` guard in `MatchTitles` |
| `internal/web/json_helpers.go` | `enrichTitlesWithMatches` fallback; add fields to `SelectedReleaseJSON` |
| `internal/web/drive_session.go` | Add ASIN, RegionCode, Locale fields to `DriveSession` |
| `internal/web/handlers_drive_select.go` | Accept and persist new identifier fields |
| `internal/workflow/orchestrator.go` | `buildTrackMetadata` accepts and applies `SelectionOpts` |
| `templates/drive_detail.templ` | Wire new fields in `doSelectRelease`; update step 2 table, step 3 and step 4 banners |

---

## Testing Notes

- `internal/discdb/matcher_test.go`: add cases for titles with and without `dt.Item` to verify new `Matched` semantics; add stub-disc fallback case.
- `internal/web/json_helpers_test.go`: add cases for `enrichTitlesWithMatches` with mixed identified/stub disc; all-stub fallback.
- `internal/workflow/orchestrator_test.go`: add cases for `buildTrackMetadata` with `SelectionOpts` filtering (audio lang, lossless, subtitle lang).
