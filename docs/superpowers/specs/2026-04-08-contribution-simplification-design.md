# Contribution Feature Simplification

## Problem

The contribution feature has accumulated significant duplication and divergent code paths that make every change a bug factory. The root cause: "add" (new release) and "update" (existing release) contributions, plus "submit" and "resubmit" operations, are implemented as 4 separate service methods with nearly identical GitHub workflow code, 3 separate handler helpers with different draft-save semantics, and 5 separate HTML forms with different hidden field sets.

Recent bugs caused by this architecture:
- ASIN/ReleaseDate/FrontImageURL silently dropped on add-type resubmit (form missing hidden fields)
- title_labels wiped to empty string on resubmit (wrong save helper called)
- ImageUrl included in release.json despite upstream maintainer saying to omit it
- Multiple prior sessions attempting fixes that introduced new regressions

### Current Architecture (What We're Replacing)

**Service layer** (780 lines across 2 files):
- `Submit()`, `Resubmit()`, `resubmitFresh()` in `service.go`
- `SubmitUpdate()`, `ResubmitUpdate()`, `resubmitUpdateFresh()` in `update_service.go`
- ~150 lines of duplicated code (JSON parsing, image download, fork/branch/PR workflow)

**Handler layer** (470 lines):
- 7 handlers + 3 helper methods
- `parseAndSaveDraft()` — overwrites all draft fields (dangerous for resubmit)
- `parseAndSaveUpdateDraft()` — patches match_info, preserves release_info
- `mergeReleaseInfoFields()` — patches release_info only (added as a bug fix)

**Template** (690 lines):
- 5 separate `<form>` elements with different hidden field sets
- Resubmit forms omit title_labels (intentionally frozen, but user wants them editable)
- `contributionFormInit()` bridges MatchInfo fields into ReleaseInfo for display

**Storage**:
- ASIN/release_date/front_image_url stored in `release_info` for add, `match_info` for update
- Every handler and template must branch on contribution_type to find these fields

## Design

### 1. Unified Storage

ASIN, release_date, and front_image_url always live in `release_info` regardless of contribution type.

**release_info** (user-editable release metadata):
```json
{
  "upc": "043396640658",
  "region_code": "A",
  "year": 1988,
  "format": "UHD",
  "slug": "1988-4k",
  "media_type": "movie",
  "asin": "B0CCZQNJ3R",
  "release_date": "1988-05-20",
  "front_image_url": "https://..."
}
```

For update contributions, only `asin`, `release_date`, and `front_image_url` are populated. The rest remain empty since they come from the existing TheDiscDB record.

**match_info** (disc match identifiers, not user-editable):
```json
{
  "media_slug": "willow-1988",
  "media_type": "movie",
  "media_title": "Willow",
  "media_year": 1988,
  "release_slug": "1988-4k",
  "disc_index": 1,
  "image_url": "Movie/willow-1988/cover.jpg"
}
```

ASIN, release_date, and front_image_url are removed from the MatchInfo struct.

**Migration 006**: For existing update contributions, copy `asin`, `release_date`, `front_image_url` from match_info JSON into release_info JSON, then strip them from match_info. This is a data-only migration (no schema change).

### 2. Handler Consolidation

**Routes** (8 → 5):
```
GET  /contributions              → handleContributions()       (unchanged)
GET  /contributions/:id          → handleContributionDetail()  (unchanged)
POST /contributions/:id          → handleContributionSave()    (simplified)
POST /contributions/:id/submit   → handleContributionSubmit()  (unified)
POST /contributions/:id/delete   → handleContributionDelete()  (unchanged)
```

Eliminated routes:
- `POST /contributions/:id/submit-update` — merged into `/submit`
- `POST /contributions/:id/resubmit` — merged into `/submit`
- `POST /contributions/:id/resubmit-update` — merged into `/submit`

**Unified save helper** — one `saveDraft()` method replaces all three current helpers:
1. Load existing contribution from DB
2. Read user-editable form fields: `asin`, `release_date`, `front_image_url`, `title_labels`
3. Parse existing `release_info` JSON from DB
4. Merge form values into the parsed struct (only overwrite non-empty fields)
5. Marshal back and call `UpdateContributionDraft(id, contrib.TmdbID, updatedReleaseInfoJSON, titleLabelsRaw)`

This always preserves non-editable fields and never blanks out data.

**Unified submit handler** — `handleContributionSubmit()`:
1. Call `saveDraft()` to persist current form state
2. Validate title labels (at least one has a type)
3. Call `svc.Execute(ctx, id)` — service determines add/update and submit/resubmit from DB state
4. Broadcast SSE event (always, for both submit and resubmit)
5. Redirect with flash message

Non-editable metadata (media_title, media_year, media_type, tmdb_id) is read from the DB by the service layer, not passed as form parameters.

### 3. Form Simplification

**2 forms** instead of 5:

**Save Draft form** (shown when status != "submitted"):
```html
<form method="POST" action="/contributions/{id}">
  <input type="hidden" name="_csrf" .../>
  <input type="hidden" name="asin" :value="$store.contrib.asin"/>
  <input type="hidden" name="release_date" :value="$store.contrib.releaseDate"/>
  <input type="hidden" name="front_image_url" :value="$store.contrib.frontImageURL"/>
  <input type="hidden" name="title_labels" value=""/>
  <button type="submit">Save Draft</button>
</form>
```

**Submit form** (shown always, button text varies):
```html
<form method="POST" action="/contributions/{id}/submit">
  <input type="hidden" name="_csrf" .../>
  <input type="hidden" name="asin" :value="$store.contrib.asin"/>
  <input type="hidden" name="release_date" :value="$store.contrib.releaseDate"/>
  <input type="hidden" name="front_image_url" :value="$store.contrib.frontImageURL"/>
  <input type="hidden" name="title_labels" value=""/>
  <button type="submit">
    <!-- "Submit to TheDiscDB" or "Update PR" based on status -->
  </button>
</form>
```

Both forms always serialize title_labels via Alpine `@submit`. The core field set (asin, release_date, front_image_url, title_labels) is identical across both forms and all contribution types.

**Add-specific fields** (tmdb_id, upc, region_code, format, year, media_type): These are only editable during add-type drafting. Both forms conditionally include them when `contribution_type == "add" && status != "submitted"`. This is the only template branch in the form section — the form action URL and core fields never change.

### 4. Service Pipeline

**Delete**: `service.go` methods `Submit()`, `Resubmit()`, `resubmitFresh()` and entire `update_service.go` file (`SubmitUpdate()`, `ResubmitUpdate()`, `resubmitUpdateFresh()`).

**New `service.go` structure:**

```go
// Execute runs the full contribution pipeline for any contribution type and status.
func (s *Service) Execute(ctx context.Context, contributionID int64) (string, error)
```

Pipeline steps:
1. **Load**: `GetContribution(id)` — fail if not found
2. **Parse**: Extract ReleaseInfo, MatchInfo (if update), TitleLabels, DiscScan from JSON columns
3. **Validate**: At least one title label has a non-empty type
4. **GitHub user**: `s.github.GetUser(ctx)`
5. **Build files**: Branch on `contribution_type`:
   - `"add"` → `buildAddFiles()` — generates release.json, disc01.json, disc01-summary.txt, disc01.txt, metadata.json, tmdb.json, cover.jpg (TMDB poster), front.jpg
   - `"update"` → `buildUpdateFiles()` — fetches existing disc JSON and merges, patches release.json, downloads cover.jpg if missing, downloads front.jpg
6. **Commit**: Branch on `status`:
   - `"pending"` / `"draft"` → `submitFresh()` — ensure fork, create branch, commit, open PR
   - `"submitted"` → `pushToExisting()` — commit to existing branch; if branch gone, fall back to `submitFresh()`; reopen PR if closed
7. **Update DB**: `UpdateContributionStatus(id, "submitted", prURL)`
8. **Return** PR URL

**File builders:**

```go
func (s *Service) buildAddFiles(ctx context.Context, c *db.Contribution,
    ri ReleaseInfo, labels []TitleLabel, scan *makemkv.DiscScan,
    githubUser string) ([]ghpkg.FileEntry, string, error)
```
Returns files + commit message. Fetches TMDB details, generates all 6 text files + images.

```go
func (s *Service) buildUpdateFiles(ctx context.Context, ri ReleaseInfo,
    mi MatchInfo, labels []TitleLabel, scan *makemkv.DiscScan,
    githubUser string) ([]ghpkg.FileEntry, string, error)
```
Returns files + commit message. Fetches existing disc JSON from upstream, merges, patches release.json, downloads images.

**GitHub workflow helpers:**

```go
func (s *Service) submitFresh(ctx context.Context, githubUser, branchName string,
    files []ghpkg.FileEntry, commitMsg, prTitle string) (string, error)
```
Ensure fork → wait → get base SHA → create branch → commit → open PR. Returns PR URL.

```go
func (s *Service) pushToExisting(ctx context.Context, githubUser, branchName string,
    files []ghpkg.FileEntry, commitMsg, prTitle, existingPRURL string) (string, error)
```
Commit to branch. If branch not found → call `submitFresh()`. If PR exists and closed → reopen.

**Shared utilities** (kept from current code):
- `downloadFromURL()` — fetch binary content from URL
- `patchReleaseJSON()` — patch ASIN/ReleaseDate into existing release.json
- `parsePRNumber()` — extract PR number from URL
- `slugify()`, `MediaDirPath()`, `ReleaseSlug()` — path utilities

### 5. Template Changes

**`contributionFormInit()`**: Remove the MatchInfo→ReleaseInfo bridge. ASIN/date/image always come from `release_info` regardless of contribution type.

**Form section**: Replace the 5-form conditional tree with 2 forms. The submit form's button text and validation message change based on `status` and `contribution_type`, but the form structure is identical.

**Alpine `isValid`**: For add contributions in draft, require tmdb_id + format + year + asin + release_date. For update contributions, require at least one typed title. For resubmit (any type), always valid (fields are already persisted).

### 6. What Stays the Same

- `filegen.go` — all file generation functions unchanged
- `update_filegen.go` — `MergeDiscJSON()` unchanged
- `types.go` — `ReleaseInfo` struct unchanged; `MatchInfo` struct has 3 fields removed
- `templates/contributions.templ` — list page unchanged
- DB schema columns — no column additions or removals
- The contribution detail page layout and UI appearance

## Verification

1. `go test ./...` passes
2. `go vet ./...` clean
3. `go build -o bluforge .` succeeds
4. Manual test: Create a new "add" contribution, fill in all fields, submit → PR opens with ASIN, release date, and front image
5. Manual test: Edit ASIN on the submitted contribution, click "Update PR" → commit includes updated ASIN in release.json
6. Manual test: Create an "update" contribution, add ASIN and front image URL, submit → PR includes patched release.json and front.jpg
7. Manual test: Edit title labels on submitted update contribution, click "Update PR" → commit reflects title label changes

## Files to Modify

| File | Change |
|------|--------|
| `migrations/006_unify_contribution_metadata.sql` | New: data migration for existing update contributions |
| `migrations/embed.go` | Update embed directive if needed |
| `internal/contribute/types.go` | Remove ASIN/ReleaseDate/FrontImageURL from MatchInfo |
| `internal/contribute/service.go` | Rewrite: Execute() pipeline, buildAddFiles(), submitFresh(), pushToExisting() |
| `internal/contribute/update_service.go` | Delete: all methods moved into service.go |
| `internal/contribute/update_filegen.go` | Keep MergeDiscJSON(); move patchReleaseJSON() to service.go |
| `internal/web/handlers_contribute.go` | Simplify: remove 4 handlers + 2 helpers, unify into saveDraft() + handleContributionSubmit() |
| `internal/web/server.go` | Remove 3 routes |
| `templates/contribution_detail.templ` | Simplify forms from 5 to 2, remove MatchInfo bridge |
| `internal/contribute/service_test.go` | Update tests for new Execute() API |
| `internal/contribute/filegen_test.go` | Minor: update if MatchInfo struct changes affect tests |
