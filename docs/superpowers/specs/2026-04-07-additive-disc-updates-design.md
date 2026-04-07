# Additive Disc Updates Design

**Date:** 2026-04-07
**Status:** Approved

## Context

BluForge already supports contributing entirely new disc entries to TheDiscDB when a scan finds no match. This design adds a second contribution type: **update contributions**, created whenever a disc scan finds a confident TheDiscDB match. This lets users correct metadata errors on already-catalogued titles (e.g. a Trailer mislabelled as Extra) and add MPLS playlist titles that were not previously catalogued.

The result is a PR against the TheDiscDB data repo that patches the existing disc JSON — appending new titles and updating item metadata on existing ones — without destroying data BluForge cannot capture (ContentHash, chapter names, track corrections made by prior contributors).

Update contributions that the user does not wish to act on can be removed using the existing "Delete" action.

Additionally, both new "add" PRs and "update" PRs will include a BluForge attribution line in the PR body.

---

## Database

One migration adds two columns to the `contributions` table:

```sql
ALTER TABLE contributions ADD COLUMN contribution_type TEXT NOT NULL DEFAULT 'add';
ALTER TABLE contributions ADD COLUMN match_info TEXT;
```

- **`contribution_type`**: `"add"` (existing behaviour) or `"update"` (new).
- **`match_info`**: JSON blob, only populated for `"update"` contributions:

```json
{
  "media_slug":    "blade-runner-2049",
  "media_type":    "movie",
  "media_title":   "Blade Runner 2049",
  "media_year":    2017,
  "release_slug":  "2018-4k-ultra-hd-blu-ray",
  "disc_index":    1,
  "image_url":     "https://..."
}
```

The `title_labels` column is reused for update contributions, pre-populated by the orchestrator from the TheDiscDB match data at scan time.

`SaveContribution` is updated to accept the two new fields. A `GetContributionsByType` helper is added for list-page badge logic.

---

## Detection Pipeline (orchestrator)

`autoMatch()` gains a third branch. After `BestRelease` returns a result:

- **No result** → existing "add" contribution path (unchanged)
- **Result found** → auto-name with matched titles as today, PLUS call `EnsureUpdateContributionRecord(scan, best)`

`EnsureUpdateContributionRecord`:
- Sets `contribution_type = "update"`
- Populates `match_info` from the `SearchResult`
- Pre-populates `title_labels` — matched titles carry their TheDiscDB type/name/season/episode; unmatched titles get `type: ""` (renders as "Omit" in the form)
- Uses the same `disc_key` SHA256 (dedup check unchanged)
- Fires the existing `contribution_available` SSE event (same payload)

The rip proceeds normally in all cases; the update contribution is created in parallel. Users who do not wish to act on it can delete it from the contributions list.

---

## Service Layer

Two new methods on `Service`: `SubmitUpdate()` and `ResubmitUpdate()`.

### `SubmitUpdate()` flow

1. Load contribution; validate `match_info` non-empty and ≥ 1 non-omitted title label
2. Parse `match_info` → media slug, release slug, disc index, media type, title, year
3. Construct GitHub path:
   `data/{media_type}/{media_title} ({media_year})/{release_slug}/disc{index:02d}.json`
4. Fetch existing disc JSON from `GET /repos/TheDiscDb/data/contents/{path}` using the user's configured GitHub token
5. Check for existing images — check `{mediaDir}/cover.jpg` and `{releaseDir}/front.jpg` via Contents API; note which are absent
6. If either image is absent: download from `match_info.image_url` to include the missing ones in the PR
7. Build merged disc JSON via `MergeDiscJSON()` (see File Generation)
8. Generate PR files: `disc{N}.json`, `disc{N}-summary.txt`, `disc{N}.txt`, plus optionally `cover.jpg` / `front.jpg`
9. Fork, create branch `{media-slug}-{release-slug}-update`, commit, open PR
10. `UpdateContributionStatus` → `"submitted"`

### `ResubmitUpdate()` flow

Re-fetches the existing disc JSON (captures any upstream changes since last submit), rebuilds the merge, pushes to the existing branch, reopens PR if closed. Mirrors `Resubmit()` exactly in structure.

### PR titles and bodies

| Type   | PR Title |
|--------|----------|
| Add    | `Add {title} ({year}) - {format}` (unchanged) |
| Update | `Update {title} ({year}) - {release_slug}` |

Both PR bodies (currently empty string) get a standard footer:

```
Submitted with the assistance of [BluForge](https://github.com/johnpostlethwait/BluForge).
```

---

## File Generation

New function in `internal/contribute/filegen.go`:

```go
func MergeDiscJSON(existing DiscJSON, scan *makemkv.DiscScan, labels []TitleLabel) DiscJSON
```

**Merge rules (by field):**

| Field | Source |
|-------|--------|
| `Index`, `Slug`, `Name`, `Format`, `ContentHash` | Existing JSON (preserved) |
| `Item.Type`, `Item.Title`, `Item.Season`, `Item.Episode` on existing titles | User's label (overwrites) |
| `Tracks`, `Chapters`, `SegmentMap`, `Duration`, `Size`, `DisplaySize` on existing titles | Existing JSON (preserved) |
| Titles in existing JSON not present in scan | Existing JSON (passed through untouched) |
| New titles (SourceFile not in existing JSON) | Full entry from scan data + user label |

Matching between existing titles and scan titles uses `SourceFile` as the key.

New helper:

```go
func CheckImages(ctx, github, mediaDir, releaseDir string) (hasCover, hasFront bool, err error)
```

Checks `{mediaDir}/cover.jpg` and `{releaseDir}/front.jpg` independently via the Contents API.

`GenerateSummary()` and all other existing filegen functions are unchanged.

---

## UI / Templates

### Contributions list (`contributions.templ`)

- "Add" contributions: no change
- "Update" contributions: distinct badge (e.g. "Update Available") and a sub-note showing the matched TheDiscDB title from `match_info`. Same "Contribute" and "Delete" actions.

### Contribution detail (`contribution_detail.templ`)

**TMDB section:** Shown only for `contribution_type == "add"`. For updates, replaced by a read-only "Matched Release" card showing media title, year, release slug, and a link to the TheDiscDB entry (constructed from `match_info` slugs).

**Titles table:** Same structure as today. For update contributions:
- Rows pre-populated from TheDiscDB data display a small "matched" visual indicator
- Rows with no TheDiscDB match display no indicator (default to "Omit")
- All rows are editable regardless of matched status

**Submit footer:** For update contributions, button label is "Submit Update to TheDiscDB". Validation requires ≥ 1 non-omitted title; does not require `tmdbID`, `format`, or `year`.

---

## Verification

1. Insert a disc that has a confident TheDiscDB match with at least one unmatched MPLS title → confirm an "update" contribution record is created and `contribution_available` SSE fires
2. Confirm no duplicate update contribution is created on disc re-insertion (disc_key dedup)
3. Open the contribution detail page → confirm matched titles are pre-filled with TheDiscDB data and visually indicated; unmatched titles default to "Omit"
4. Edit a matched title type, submit → confirm the PR contains an updated `disc{N}.json` where only `Item.*` changed on that title, and `ContentHash` / `Tracks` / `Chapters` are preserved from the fetched JSON
5. Confirm new unmatched titles appear as full new entries in the merged JSON
6. Confirm the PR title starts with "Update" and the body contains the BluForge attribution link
7. Insert a disc where all titles are matched → confirm an update contribution is still created (all titles pre-filled, none default to "Omit")
8. Confirm "add" PRs also now include the BluForge attribution line
9. Test resubmit: close the PR branch, hit "Update PR" → confirm a new branch and PR are created (branch-not-found recovery path)
10. Test image handling: mock a release directory with no images → confirm images are included in PR; mock one with existing images → confirm images are omitted
