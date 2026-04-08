# Contribution Feature Simplification — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Collapse 6 duplicated service methods, 7 handlers, and 5 forms into a single `Execute()` pipeline, 2 handlers, and 2 forms — eliminating the root cause of repeated contribution bugs.

**Architecture:** One `Execute()` method orchestrates the full pipeline. Add-vs-update is a data difference (which file builder to call), not a code path difference. Submit-vs-resubmit is determined by checking the contribution's DB status. Storage is unified: ASIN/date/image always live in `release_info`.

**Tech Stack:** Go, templ, Alpine.js, SQLite, GitHub API

**Spec:** `docs/superpowers/specs/2026-04-08-contribution-simplification-design.md`

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `migrations/006_unify_contribution_metadata.sql` | Create | Data migration: copy ASIN/date/image from match_info to release_info for update contributions |
| `internal/contribute/types.go` | Modify | Remove ASIN/ReleaseDate/FrontImageURL from MatchInfo struct |
| `internal/contribute/service.go` | Rewrite | Execute() pipeline, buildAddFiles(), submitFresh(), pushToExisting(), shared utilities |
| `internal/contribute/update_service.go` | Delete | All methods absorbed into service.go |
| `internal/contribute/update_filegen.go` | Modify | Keep MergeDiscJSON(); move patchReleaseJSON()+downloadFromURL() to service.go |
| `internal/web/handlers_contribute.go` | Rewrite | Unified saveDraft(), handleContributionSubmit(); remove 4 handlers + 2 helpers |
| `internal/web/server.go` | Modify | Remove 3 routes |
| `templates/contribution_detail.templ` | Modify | Simplify from 5 forms to 2; remove MatchInfo→ReleaseInfo bridge |
| `internal/contribute/service_test.go` | Rewrite | Update all tests for Execute() API |

---

## Task 1: Data Migration — Unify ASIN/Date/Image Storage

**Files:**
- Create: `migrations/006_unify_contribution_metadata.sql`

This migration copies ASIN, release_date, and front_image_url from match_info into release_info for existing update contributions, then strips those fields from match_info.

- [ ] **Step 1: Write the migration SQL**

Create `migrations/006_unify_contribution_metadata.sql`. SQLite doesn't have native JSON functions in pure-Go drivers, so this is a Go-executed migration using the application's JSON handling. However, the migrations in this project are plain SQL files executed via `embed.FS`. Since SQLite's JSON support depends on the driver, we'll write the migration as a no-op SQL marker and handle the data migration in Go application code on startup.

Actually — checking the project: migrations are embedded SQL files auto-run on startup via `migrations/embed.go`. The pure-Go SQLite driver (`modernc.org/sqlite`) includes JSON1 extension support. So we can use `json_extract` and `json_set`/`json_remove`.

```sql
-- 006_unify_contribution_metadata.sql
-- For update contributions, copy asin/release_date/front_image_url from match_info
-- into release_info, then strip them from match_info.
-- This unifies storage so ASIN/date/image always live in release_info.

UPDATE contributions
SET release_info = json_set(
      CASE WHEN release_info = '' THEN '{}' ELSE release_info END,
      '$.asin', json_extract(match_info, '$.asin'),
      '$.release_date', json_extract(match_info, '$.release_date'),
      '$.front_image_url', json_extract(match_info, '$.front_image_url')
    ),
    match_info = json_remove(match_info, '$.asin', '$.release_date', '$.front_image_url')
WHERE contribution_type = 'update'
  AND match_info != ''
  AND (
    json_extract(match_info, '$.asin') IS NOT NULL
    OR json_extract(match_info, '$.release_date') IS NOT NULL
    OR json_extract(match_info, '$.front_image_url') IS NOT NULL
  );
```

- [ ] **Step 2: Verify migration embed picks it up**

The `migrations/embed.go` uses `//go:embed *.sql` which automatically includes any new `.sql` file. No changes needed.

Run:
```bash
go build -o bluforge .
```
Expected: Builds successfully.

- [ ] **Step 3: Commit**

```bash
git add migrations/006_unify_contribution_metadata.sql
git commit -m "feat(db): add migration to unify ASIN/date/image into release_info"
```

---

## Task 2: Update MatchInfo Type — Remove Migrated Fields

**Files:**
- Modify: `internal/contribute/types.go:18-29`
- Test: `go test ./internal/contribute/ -run TestGenerate`

- [ ] **Step 1: Remove ASIN/ReleaseDate/FrontImageURL from MatchInfo**

In `internal/contribute/types.go`, replace the MatchInfo struct:

```go
// MatchInfo holds the TheDiscDB identifiers for a matched disc release.
// Stored as JSON in contributions.match_info; only set for contribution_type == "update".
// User-editable fields (ASIN, ReleaseDate, FrontImageURL) are stored in release_info, not here.
type MatchInfo struct {
	MediaSlug   string `json:"media_slug"`
	MediaType   string `json:"media_type"`
	MediaTitle  string `json:"media_title"`
	MediaYear   int    `json:"media_year"`
	ReleaseSlug string `json:"release_slug"`
	DiscIndex   int    `json:"disc_index"`
	ImageURL    string `json:"image_url"`
}
```

- [ ] **Step 2: Fix all compile errors from removed fields**

Run:
```bash
go build ./... 2>&1
```

This will show errors in `update_service.go` (references to `mi.ASIN`, `mi.ReleaseDate`, `mi.FrontImageURL`), `handlers_contribute.go` (references to `mi.ASIN`, `mi.FrontImageURL`, `mi.ReleaseDate`), and `templates/contribution_detail.templ` (the MatchInfo→ReleaseInfo bridge). **Do NOT fix these yet** — they will be replaced in later tasks. For now, just note the compile errors as expected.

- [ ] **Step 3: Commit (types only)**

```bash
git add internal/contribute/types.go
git commit -m "refactor(contribute): remove ASIN/date/image from MatchInfo struct"
```

---

## Task 3: Rewrite Service Layer — Unified Execute() Pipeline

**Files:**
- Rewrite: `internal/contribute/service.go`
- Delete: `internal/contribute/update_service.go` (move utilities to service.go)
- Modify: `internal/contribute/update_filegen.go` (keep MergeDiscJSON only)

This is the largest task. The new `service.go` replaces 6 methods with a single `Execute()` plus focused helpers.

- [ ] **Step 1: Move utilities from update_service.go into service.go**

Append `patchReleaseJSON()` and `downloadFromURL()` to the end of `service.go`. These functions change slightly — `patchReleaseJSON` now takes `ri ReleaseInfo` instead of `mi MatchInfo` since ASIN/ReleaseDate now live in ReleaseInfo:

```go
// patchReleaseJSON fetches the existing release.json from the upstream repo,
// patches in any ASIN and/or ReleaseDate from ri, and returns the updated JSON.
// Returns an empty string (no error) when the file does not exist upstream.
func patchReleaseJSON(ctx context.Context, gh GitHubClient, ri ReleaseInfo, releasePath string) (string, error) {
	exists, err := gh.FileExists(ctx, upstreamOwner, upstreamRepo, releasePath)
	if err != nil {
		return "", fmt.Errorf("check release.json existence: %w", err)
	}
	if !exists {
		return "", nil
	}

	existingJSON, err := gh.GetFileContent(ctx, upstreamOwner, upstreamRepo, releasePath)
	if err != nil {
		return "", fmt.Errorf("fetch release.json: %w", err)
	}

	var rel ReleaseJSON
	if err := json.Unmarshal([]byte(existingJSON), &rel); err != nil {
		return "", fmt.Errorf("parse release.json: %w", err)
	}

	changed := false
	if ri.ASIN != "" && rel.Asin != ri.ASIN {
		rel.Asin = ri.ASIN
		changed = true
	}
	if ri.ReleaseDate != "" {
		if t, err := time.Parse("2006-01-02", ri.ReleaseDate); err == nil {
			formatted := t.UTC().Format(time.RFC3339)
			if rel.ReleaseDate != formatted {
				rel.ReleaseDate = formatted
				changed = true
			}
		}
	}

	if !changed {
		return "", nil
	}

	data, err := json.MarshalIndent(rel, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal patched release.json: %w", err)
	}
	return string(data) + "\n", nil
}

// downloadFromURL fetches the content at url and returns the raw bytes.
func downloadFromURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("download: create request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: unexpected status %d for %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download: read body: %w", err)
	}
	return data, nil
}
```

- [ ] **Step 2: Write the unified Execute() method**

Replace `Submit()`, `Resubmit()`, and `resubmitFresh()` with:

```go
// Execute runs the full contribution pipeline for any contribution type and status.
// For "add" contributions it generates all files from scratch.
// For "update" contributions it fetches existing data from upstream and merges.
// For already-submitted contributions it pushes a corrective commit to the existing PR branch.
func (s *Service) Execute(ctx context.Context, contributionID int64) (string, error) {
	// 1. Load contribution.
	c, err := s.store.GetContribution(contributionID)
	if err != nil {
		return "", fmt.Errorf("contribute: load contribution %d: %w", contributionID, err)
	}
	if c == nil {
		return "", fmt.Errorf("contribute: contribution %d not found", contributionID)
	}

	// 2. Parse stored JSON.
	var ri ReleaseInfo
	if c.ReleaseInfo != "" {
		if err := json.Unmarshal([]byte(c.ReleaseInfo), &ri); err != nil {
			return "", fmt.Errorf("contribute: parse release_info: %w", err)
		}
	}

	var labels []TitleLabel
	if c.TitleLabels != "" {
		if err := json.Unmarshal([]byte(c.TitleLabels), &labels); err != nil {
			return "", fmt.Errorf("contribute: parse title_labels: %w", err)
		}
	}

	var scan makemkv.DiscScan
	if err := json.Unmarshal([]byte(c.ScanJSON), &scan); err != nil {
		return "", fmt.Errorf("contribute: parse scan_json: %w", err)
	}

	// 3. Validate: at least one title label has a non-empty type.
	hasTyped := false
	for _, l := range labels {
		if l.Type != "" {
			hasTyped = true
			break
		}
	}
	if !hasTyped {
		return "", fmt.Errorf("contribute: contribution %d has no typed title label — assign at least one title type", contributionID)
	}

	// 4. Get GitHub user.
	githubUser, err := s.github.GetUser(ctx)
	if err != nil {
		return "", fmt.Errorf("contribute: get github user: %w", err)
	}

	// 5. Build files — branch on contribution type.
	var files []ghpkg.FileEntry
	var branchName, commitMsg, prTitle string

	switch c.ContributionType {
	case "update":
		var mi MatchInfo
		if c.MatchInfo == "" {
			return "", fmt.Errorf("contribute: contribution %d has no match_info — complete the draft first", contributionID)
		}
		if err := json.Unmarshal([]byte(c.MatchInfo), &mi); err != nil {
			return "", fmt.Errorf("contribute: parse match_info: %w", err)
		}
		files, branchName, commitMsg, prTitle, err = s.buildUpdateFiles(ctx, c, ri, mi, labels, &scan, githubUser)
	default: // "add"
		if c.TmdbID == "" {
			return "", fmt.Errorf("contribute: contribution %d has no tmdb_id — complete the draft first", contributionID)
		}
		if c.ReleaseInfo == "" {
			return "", fmt.Errorf("contribute: contribution %d has no release_info — complete the draft first", contributionID)
		}
		files, branchName, commitMsg, prTitle, err = s.buildAddFiles(ctx, c, ri, labels, &scan, githubUser)
	}
	if err != nil {
		return "", err
	}

	// 6. Commit — branch on status.
	var prURL string
	if c.Status == "submitted" {
		prURL, err = s.pushToExisting(ctx, contributionID, githubUser, branchName, files, commitMsg, prTitle, c.PRURL)
	} else {
		prURL, err = s.submitFresh(ctx, githubUser, branchName, files, commitMsg, prTitle)
	}
	if err != nil {
		return "", err
	}

	// 7. Update DB status.
	if err := s.store.UpdateContributionStatus(contributionID, "submitted", prURL); err != nil {
		return "", fmt.Errorf("contribute: update status: %w", err)
	}

	return prURL, nil
}
```

- [ ] **Step 3: Write buildAddFiles()**

```go
// buildAddFiles generates all files for a new "add" contribution.
func (s *Service) buildAddFiles(ctx context.Context, c *db.Contribution,
	ri ReleaseInfo, labels []TitleLabel, scan *makemkv.DiscScan,
	githubUser string) ([]ghpkg.FileEntry, string, string, string, error) {

	tmdbIDInt, err := strconv.Atoi(c.TmdbID)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: tmdb_id %q is not a valid integer: %w", c.TmdbID, err)
	}

	mediaType := ri.MediaType
	if mediaType == "" {
		mediaType = "movie"
	}

	tmdbRaw, tmdbDetails, err := s.tmdb.GetDetails(ctx, tmdbIDInt, mediaType)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: fetch TMDB details for id %d: %w", tmdbIDInt, err)
	}

	var posterBytes []byte
	if tmdbDetails.PosterPath != "" {
		imgBytes, imgErr := s.tmdb.DownloadImage(ctx, tmdbDetails.PosterPath, "original")
		if imgErr != nil {
			slog.Warn("contribute: failed to download TMDB poster; submission will proceed without images",
				"poster_path", tmdbDetails.PosterPath, "error", imgErr)
		} else {
			posterBytes = imgBytes
		}
	}

	mediaTitle := c.DiscName
	mediaYear := ri.Year
	titleSlug := slugify(mediaTitle, mediaYear)
	releaseSlug := ReleaseSlug(ri.Year, ri.Format)
	mediaDir := MediaDirPath(mediaType, mediaTitle, mediaYear)
	releaseDir := mediaDir + "/" + releaseSlug

	files := []ghpkg.FileEntry{
		{Path: releaseDir + "/release.json", Content: GenerateReleaseJSON(ri, githubUser)},
		{Path: releaseDir + "/disc01.json", Content: GenerateDiscJSON(scan, ri.Format)},
		{Path: releaseDir + "/disc01-summary.txt", Content: GenerateSummary(scan, labels)},
		{Path: releaseDir + "/disc01.txt", Content: c.RawOutput},
		{Path: mediaDir + "/metadata.json", Content: GenerateMetadataJSON(tmdbDetails, mediaType, mediaTitle, mediaYear)},
		{Path: mediaDir + "/tmdb.json", Content: string(tmdbRaw)},
	}
	if len(posterBytes) > 0 {
		files = append(files, ghpkg.FileEntry{Path: mediaDir + "/cover.jpg", Blob: posterBytes})
	}
	if ri.FrontImageURL != "" {
		frontBytes, frontErr := downloadFromURL(ctx, ri.FrontImageURL)
		if frontErr != nil {
			slog.Warn("contribute: failed to download front cover image; submission will proceed without front.jpg",
				"front_image_url", ri.FrontImageURL, "error", frontErr)
		} else if len(frontBytes) > 0 {
			files = append(files, ghpkg.FileEntry{Path: releaseDir + "/front.jpg", Blob: frontBytes})
		}
	}

	branchName := ghpkg.ContributionBranchName(titleSlug, releaseSlug)
	commitMsg := fmt.Sprintf("Add %s (%d) - %s", mediaTitle, mediaYear, ri.Format)
	prTitle := commitMsg

	// Use "Fix" prefix for resubmits (status already submitted).
	if c.Status == "submitted" {
		commitMsg = fmt.Sprintf("Fix %s (%d) - %s: regenerate all contribution files", mediaTitle, mediaYear, ri.Format)
	}

	return files, branchName, commitMsg, prTitle, nil
}
```

- [ ] **Step 4: Write buildUpdateFiles()**

```go
// buildUpdateFiles generates files for an "update" contribution by fetching
// existing data from upstream and merging user edits.
func (s *Service) buildUpdateFiles(ctx context.Context, c *db.Contribution,
	ri ReleaseInfo, mi MatchInfo, labels []TitleLabel, scan *makemkv.DiscScan,
	githubUser string) ([]ghpkg.FileEntry, string, string, string, error) {

	mediaDir := MediaDirPath(mi.MediaType, mi.MediaTitle, mi.MediaYear)
	releaseDir := mediaDir + "/" + mi.ReleaseSlug
	discFileName := fmt.Sprintf("disc%02d.json", mi.DiscIndex)
	discPath := releaseDir + "/" + discFileName

	// Fetch existing disc JSON from upstream and merge.
	existingDiscJSON, err := s.github.GetFileContent(ctx, upstreamOwner, upstreamRepo, discPath)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: fetch existing disc JSON at %s: %w", discPath, err)
	}
	merged, err := MergeDiscJSON(existingDiscJSON, scan, labels)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: merge disc JSON: %w", err)
	}
	mergedBytes, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: marshal merged disc JSON: %w", err)
	}

	files := []ghpkg.FileEntry{
		{Path: discPath, Content: string(mergedBytes) + "\n"},
	}

	// Cover image: only upload if missing from upstream.
	coverPath := mediaDir + "/cover.jpg"
	coverExists, err := s.github.FileExists(ctx, upstreamOwner, upstreamRepo, coverPath)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("contribute: check cover image existence: %w", err)
	}
	if !coverExists && mi.ImageURL != "" {
		imgBytes, imgErr := downloadFromURL(ctx, mi.ImageURL)
		if imgErr != nil {
			slog.Warn("contribute: failed to download cover image; submission will proceed without cover.jpg",
				"image_url", mi.ImageURL, "error", imgErr)
		} else if len(imgBytes) > 0 {
			files = append(files, ghpkg.FileEntry{Path: coverPath, Blob: imgBytes})
		}
	}

	// Front image: always upload if the user supplied a URL.
	frontPath := releaseDir + "/front.jpg"
	if ri.FrontImageURL != "" {
		frontBytes, frontErr := downloadFromURL(ctx, ri.FrontImageURL)
		if frontErr != nil {
			slog.Warn("contribute: failed to download front cover image; submission will proceed without front.jpg",
				"front_image_url", ri.FrontImageURL, "error", frontErr)
		} else if len(frontBytes) > 0 {
			files = append(files, ghpkg.FileEntry{Path: frontPath, Blob: frontBytes})
		}
	}

	// Patch release.json with ASIN and/or ReleaseDate if provided.
	if ri.ASIN != "" || ri.ReleaseDate != "" {
		releasePath := releaseDir + "/release.json"
		if patchedRelease, err := patchReleaseJSON(ctx, s.github, ri, releasePath); err != nil {
			slog.Warn("contribute: failed to patch release.json; submission will proceed without it",
				"path", releasePath, "error", err)
		} else if patchedRelease != "" {
			files = append(files, ghpkg.FileEntry{Path: releasePath, Content: patchedRelease})
		}
	}

	titleSlug := slugify(mi.MediaTitle, mi.MediaYear)
	branchName := ghpkg.ContributionBranchName(titleSlug, mi.ReleaseSlug) + "-update"
	commitMsg := fmt.Sprintf("Update %s (%d) - %s", mi.MediaTitle, mi.MediaYear, mi.ReleaseSlug)
	prTitle := commitMsg

	if c.Status == "submitted" {
		commitMsg = fmt.Sprintf("Fix %s (%d) - %s: regenerate update contribution files", mi.MediaTitle, mi.MediaYear, mi.ReleaseSlug)
	}

	return files, branchName, commitMsg, prTitle, nil
}
```

- [ ] **Step 5: Write submitFresh()**

```go
// submitFresh forks the upstream repo, creates a branch, commits files, and opens a PR.
func (s *Service) submitFresh(ctx context.Context, githubUser, branchName string,
	files []ghpkg.FileEntry, commitMsg, prTitle string) (string, error) {

	fork, err := s.github.EnsureFork(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return "", fmt.Errorf("contribute: ensure fork: %w", err)
	}
	forkOwner := strings.SplitN(fork, "/", 2)[0]

	if err := s.github.WaitForRepo(ctx, forkOwner, upstreamRepo); err != nil {
		return "", fmt.Errorf("contribute: wait for fork: %w", err)
	}

	baseBranch, baseSHA, err := s.github.GetDefaultBranchSHA(ctx, upstreamOwner, upstreamRepo)
	if err != nil {
		return "", fmt.Errorf("contribute: get default branch SHA: %w", err)
	}

	if err := s.github.CreateBranch(ctx, forkOwner, upstreamRepo, branchName, baseSHA); err != nil {
		if !strings.Contains(err.Error(), "Reference already exists") {
			return "", fmt.Errorf("contribute: create branch: %w", err)
		}
	}

	if err := s.github.CommitFiles(ctx, forkOwner, upstreamRepo, branchName, files, commitMsg); err != nil {
		return "", fmt.Errorf("contribute: commit files: %w", err)
	}

	prHead := githubUser + ":" + branchName
	prURL, err := s.github.CreatePR(ctx, upstreamOwner, upstreamRepo, prHead, baseBranch, prTitle, prBody)
	if err != nil {
		return "", fmt.Errorf("contribute: create PR: %w", err)
	}

	return prURL, nil
}
```

- [ ] **Step 6: Write pushToExisting()**

```go
// pushToExisting commits to an existing PR branch. If the branch no longer exists,
// it falls back to submitFresh(). If the PR is closed, it attempts to reopen it.
func (s *Service) pushToExisting(ctx context.Context, contributionID int64, githubUser, branchName string,
	files []ghpkg.FileEntry, commitMsg, prTitle, existingPRURL string) (string, error) {

	err := s.github.CommitFiles(ctx, githubUser, upstreamRepo, branchName, files, commitMsg)
	if errors.Is(err, ghpkg.ErrBranchNotFound) {
		slog.Info("contribute: branch not found, recreating branch and opening new PR",
			"branch", branchName)
		return s.submitFresh(ctx, githubUser, branchName, files, commitMsg, prTitle)
	}
	if err != nil {
		return "", fmt.Errorf("contribute: commit files: %w", err)
	}

	// Reopen the PR if it was closed.
	if prNum := parsePRNumber(existingPRURL); prNum > 0 {
		if rerr := s.github.ReopenPR(ctx, upstreamOwner, upstreamRepo, prNum); rerr != nil {
			slog.Warn("contribute: could not reopen PR; files were pushed but PR may still be closed",
				"pr_url", existingPRURL, "error", rerr)
		}
	}

	return existingPRURL, nil
}
```

- [ ] **Step 7: Keep parsePRNumber() and slugify() — remove old methods**

Keep `parsePRNumber()`, `slugify()`, `nonAlphanumHyphen`, `multiHyphen` exactly as they are.

Delete the old method bodies: `Submit()`, `Resubmit()`, `resubmitFresh()`.

- [ ] **Step 8: Delete update_service.go**

Remove the file entirely. All its logic has been absorbed into the new `service.go` methods.

- [ ] **Step 9: Clean up update_filegen.go**

Remove `patchReleaseJSON()` and `downloadFromURL()` from `update_filegen.go` (they're now in `service.go`). Keep only `MergeDiscJSON()`.

- [ ] **Step 10: Update imports**

Ensure `service.go` has all needed imports:
```go
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/johnpostlethwait/bluforge/internal/db"
	ghpkg "github.com/johnpostlethwait/bluforge/internal/github"
	"github.com/johnpostlethwait/bluforge/internal/makemkv"
	"github.com/johnpostlethwait/bluforge/internal/tmdb"
)
```

Ensure `update_filegen.go` only imports what `MergeDiscJSON` needs.

- [ ] **Step 11: Verify it compiles (expect handler errors)**

```bash
go build ./internal/contribute/... 2>&1
```

The `internal/contribute` package should compile. Handler errors in `internal/web` are expected and will be fixed in Task 4.

- [ ] **Step 12: Commit**

```bash
git add internal/contribute/service.go internal/contribute/update_filegen.go
git rm internal/contribute/update_service.go
git commit -m "refactor(contribute): unified Execute() pipeline replaces 6 service methods"
```

---

## Task 4: Rewrite Handlers — Unified Save + Submit

**Files:**
- Rewrite: `internal/web/handlers_contribute.go`
- Modify: `internal/web/server.go:159-166`

- [ ] **Step 1: Remove old routes from server.go**

In `internal/web/server.go`, remove lines for the 3 eliminated routes. The route block should become:

```go
e.GET("/contributions", s.handleContributions)
e.GET("/contributions/:id", s.handleContributionDetail)
e.POST("/contributions/:id", s.handleContributionSave)
e.POST("/contributions/:id/submit", s.handleContributionSubmit)
e.POST("/contributions/:id/delete", s.handleContributionDelete)
```

- [ ] **Step 2: Rewrite handlers_contribute.go**

Replace the entire file. Keep `parseContribID()`, `handleContributions()`, `handleContributionDetail()`, `handleContributionDelete()` unchanged. Rewrite the save/submit handlers:

```go
// saveDraft reads user-editable form fields and merges them into the contribution's
// existing release_info, preserving non-editable fields. Always persists title_labels.
func (s *Server) saveDraft(c echo.Context, id int64) error {
	contrib, err := s.store.GetContribution(id)
	if err != nil || contrib == nil {
		slog.Error("failed to load contribution for draft save", "id", id)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load contribution.")
	}

	// Parse existing release_info so we can merge without overwriting.
	var ri contribute.ReleaseInfo
	if contrib.ReleaseInfo != "" {
		if err := json.Unmarshal([]byte(contrib.ReleaseInfo), &ri); err != nil {
			slog.Error("failed to parse release_info for draft save", "id", id, "error", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load contribution.")
		}
	}

	// Merge user-editable fields from form (only overwrite if provided).
	if v := c.FormValue("asin"); v != "" {
		ri.ASIN = v
	}
	if v := c.FormValue("release_date"); v != "" {
		ri.ReleaseDate = v
	}
	if v := c.FormValue("front_image_url"); v != "" {
		ri.FrontImageURL = v
	}

	// Add-specific fields (only present on add draft forms).
	if v := c.FormValue("upc"); v != "" {
		ri.UPC = v
	}
	if v := c.FormValue("region_code"); v != "" {
		ri.RegionCode = v
	}
	if v := c.FormValue("format"); v != "" {
		ri.Format = v
		ri.Slug = contribute.ReleaseSlug(ri.Year, ri.Format)
	}
	if v := c.FormValue("media_type"); v != "" {
		ri.MediaType = v
	}
	if v := c.FormValue("year"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ri.Year = n
			if ri.Format != "" {
				ri.Slug = contribute.ReleaseSlug(ri.Year, ri.Format)
			}
		}
	}

	riBytes, err := json.Marshal(ri)
	if err != nil {
		slog.Error("failed to marshal release_info", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save draft.")
	}

	// tmdb_id: use form value if present, otherwise preserve existing.
	tmdbID := c.FormValue("tmdb_id")
	if tmdbID == "" {
		tmdbID = contrib.TmdbID
	}

	// title_labels: use form value if present, otherwise preserve existing.
	titleLabels := c.FormValue("title_labels")
	if titleLabels == "" {
		titleLabels = contrib.TitleLabels
	}

	if err := s.store.UpdateContributionDraft(id, tmdbID, string(riBytes), titleLabels); err != nil {
		slog.Error("failed to update contribution draft", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to save draft.")
	}

	return nil
}

// handleContributionSave saves a draft contribution from form values.
func (s *Server) handleContributionSave(c echo.Context) error {
	id, err := parseContribID(c)
	if err != nil {
		return err
	}

	if err := s.saveDraft(c, id); err != nil {
		return err
	}

	return c.Redirect(http.StatusSeeOther, fmt.Sprintf("/contributions/%d", id))
}

// handleContributionSubmit handles submit and resubmit for both add and update contributions.
func (s *Server) handleContributionSubmit(c echo.Context) error {
	id, err := parseContribID(c)
	if err != nil {
		return err
	}

	cfg := s.GetConfig()
	if cfg.GitHubToken == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "GitHub token is not configured. Please set it in Settings.")
	}

	// Persist the current form state before submitting.
	if err := s.saveDraft(c, id); err != nil {
		return err
	}

	// Validate title labels.
	titleLabelsRaw := c.FormValue("title_labels")
	if titleLabelsRaw == "" {
		// If form didn't carry title_labels, load from DB (already persisted by saveDraft).
		contrib, err := s.store.GetContribution(id)
		if err != nil || contrib == nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Failed to load contribution.")
		}
		titleLabelsRaw = contrib.TitleLabels
	}
	var titleLabels []contribute.TitleLabel
	if titleLabelsRaw != "" {
		if err := json.Unmarshal([]byte(titleLabelsRaw), &titleLabels); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid title labels.")
		}
	}
	hasTyped := false
	for _, l := range titleLabels {
		if l.Type != "" {
			hasTyped = true
			break
		}
	}
	if !hasTyped {
		return echo.NewHTTPError(http.StatusBadRequest, "At least one title must have a type assigned before submitting.")
	}

	ghClient, err := ghpkg.NewClient(cfg.GitHubToken)
	if err != nil {
		slog.Error("failed to create GitHub client", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to create GitHub client.")
	}

	tmdbOpts := []tmdb.Option{}
	if s.tmdbBaseURL != "" {
		tmdbOpts = append(tmdbOpts, tmdb.WithBaseURL(s.tmdbBaseURL))
	}
	tmdbClient := tmdb.NewClient(cfg.TMDBApiKey, tmdbOpts...)
	svc := contribute.NewService(s.store, ghClient, tmdbClient)
	prURL, err := svc.Execute(c.Request().Context(), id)
	if err != nil {
		slog.Error("failed to execute contribution", "id", id, "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to submit contribution: "+err.Error())
	}

	sseData, _ := json.Marshal(map[string]any{
		"id":    id,
		"prURL": prURL,
	})
	s.sseHub.Broadcast(SSEEvent{
		Event: "contribution_submitted",
		Data:  string(sseData),
	})

	return c.Redirect(http.StatusSeeOther, "/contributions?flash=Contribution+submitted+%E2%80%94+PR+opened+successfully")
}
```

Delete: `parseAndSaveDraft()`, `parseAndSaveUpdateDraft()`, `mergeReleaseInfoFields()`, `handleContributionResubmit()`, `handleContributionSubmitUpdate()`, `handleContributionResubmitUpdate()`.

- [ ] **Step 3: Verify it compiles (expect template errors)**

```bash
go build ./... 2>&1
```

Handler + service should compile. Template errors are expected (old route references) and will be fixed in Task 5.

- [ ] **Step 4: Commit**

```bash
git add internal/web/handlers_contribute.go internal/web/server.go
git commit -m "refactor(web): unified saveDraft() and handleContributionSubmit() replace 7 handlers"
```

---

## Task 5: Simplify Template — 2 Forms, No MatchInfo Bridge

**Files:**
- Modify: `templates/contribution_detail.templ`
- Regenerate: `templates/contribution_detail_templ.go` (via `templ generate`)

- [ ] **Step 1: Remove the MatchInfo→ReleaseInfo bridge in contributionFormInit()**

In `templates/contribution_detail.templ`, find the block at approximately lines 55-70:

```go
// For update contributions, ASIN and FrontImageURL are stored in match_info.
var mi contribute.MatchInfo
if c.ContributionType == "update" && c.MatchInfo != "" {
	if err := json.Unmarshal([]byte(c.MatchInfo), &mi); err != nil {
		slog.Warn("contribution_detail: failed to parse match_info", "id", c.ID, "error", err)
	}
	if mi.ASIN != "" {
		ri.ASIN = mi.ASIN
	}
	if mi.ReleaseDate != "" {
		ri.ReleaseDate = mi.ReleaseDate
	}
	if mi.FrontImageURL != "" {
		ri.FrontImageURL = mi.FrontImageURL
	}
}
```

Remove this entire block. ASIN/date/image now always come from `release_info`.

- [ ] **Step 2: Simplify the form section**

Replace the 5-form conditional tree (approximately lines 555-690) with 2 forms:

**Save Draft form** (only shown when not submitted, uses the same structure for add and update):

```templ
if data.Contribution.Status != "submitted" {
	<!-- Save Draft form -->
	<form method="POST" action={ templ.SafeURL(fmt.Sprintf("/contributions/%d", data.Contribution.ID)) }
		x-data="{ saving: false }"
		@submit="document.getElementById('title_labels_save').value = $store.contrib.serializedLabels(); saving = true">
		<input type="hidden" name="_csrf" value={ data.CSRFToken }/>
		if data.Contribution.ContributionType != "update" {
			<input type="hidden" name="tmdb_id" :value="$store.contrib.tmdbID"/>
			<input type="hidden" name="upc" :value="$store.contrib.upc"/>
			<input type="hidden" name="region_code" :value="$store.contrib.regionCode"/>
			<input type="hidden" name="format" :value="$store.contrib.format"/>
			<input type="hidden" name="year" :value="$store.contrib.year"/>
			<input type="hidden" name="media_type" :value="$store.contrib.mediaType"/>
		}
		<input type="hidden" name="asin" :value="$store.contrib.asin"/>
		<input type="hidden" name="release_date" :value="$store.contrib.releaseDate"/>
		<input type="hidden" name="front_image_url" :value="$store.contrib.frontImageURL"/>
		<input type="hidden" id="title_labels_save" name="title_labels" value=""/>
		<button type="submit" class="btn" :disabled="saving">
			<span x-show="!saving">Save Draft</span>
			<span x-show="saving" x-cloak class="flex items-center gap-2">
				<span class="spinner" aria-hidden="true"></span> Saving&hellip;
			</span>
		</button>
	</form>
}

<!-- Submit / Update PR form -->
<form method="POST" action={ templ.SafeURL(fmt.Sprintf("/contributions/%d/submit", data.Contribution.ID)) }
	x-data="{ submitting: false }"
	@submit="document.getElementById('title_labels_submit').value = $store.contrib.serializedLabels(); submitting = true">
	<input type="hidden" name="_csrf" value={ data.CSRFToken }/>
	if data.Contribution.ContributionType != "update" && data.Contribution.Status != "submitted" {
		<input type="hidden" name="tmdb_id" :value="$store.contrib.tmdbID"/>
		<input type="hidden" name="upc" :value="$store.contrib.upc"/>
		<input type="hidden" name="region_code" :value="$store.contrib.regionCode"/>
		<input type="hidden" name="format" :value="$store.contrib.format"/>
		<input type="hidden" name="year" :value="$store.contrib.year"/>
		<input type="hidden" name="media_type" :value="$store.contrib.mediaType"/>
	}
	<input type="hidden" name="asin" :value="$store.contrib.asin"/>
	<input type="hidden" name="release_date" :value="$store.contrib.releaseDate"/>
	<input type="hidden" name="front_image_url" :value="$store.contrib.frontImageURL"/>
	<input type="hidden" id="title_labels_submit" name="title_labels" value=""/>
	<button type="submit" class="btn btn-primary"
		if !data.GitHubTokenConfigured {
			disabled
		}
		:disabled="submitting || !$store.contrib.isValid"
		aria-describedby="contrib-validation-msg github-token-warning">
		if data.Contribution.Status == "submitted" {
			<span x-show="!submitting">Update PR</span>
		} else {
			<span x-show="!submitting">Submit to TheDiscDB</span>
		}
		<span x-show="submitting" x-cloak class="flex items-center gap-2">
			<span class="spinner" aria-hidden="true"></span> Submitting&hellip;
		</span>
	</button>
</form>

if data.Contribution.Status != "submitted" {
	<span id="contrib-validation-msg" class="text-muted text-sm" x-show="!$store.contrib.isValid" x-cloak>
		if data.Contribution.ContributionType == "update" {
			At least one title must have a type assigned before submitting.
		} else {
			TMDB ID, format, year, ASIN, and release date are required before submitting.
		}
	</span>
} else {
	<span class="text-muted text-sm">
		Regenerates and pushes corrected files to the PR branch, reopening it if closed.
	</span>
}

if data.Contribution.PRURL != "" {
	<a href={ templ.SafeURL(data.Contribution.PRURL) } target="_blank" rel="noopener" class="btn">View PR on GitHub</a>
}
```

- [ ] **Step 3: Regenerate templ**

```bash
templ generate
```

Or if `templ` is not on PATH:

```bash
~/go/bin/templ generate
```

- [ ] **Step 4: Verify full build**

```bash
go build -o bluforge .
```

Expected: Builds successfully with no errors.

- [ ] **Step 5: Commit**

```bash
git add templates/contribution_detail.templ templates/contribution_detail_templ.go
git commit -m "refactor(templates): simplify contribution forms from 5 to 2"
```

---

## Task 6: Update Tests

**Files:**
- Rewrite: `internal/contribute/service_test.go`

The existing tests call `Submit()`, `Resubmit()`, `SubmitUpdate()`, `ResubmitUpdate()` directly. All of these now go through `Execute()`. The test structure stays the same (same scenarios), but the API surface changes.

- [ ] **Step 1: Update test helpers**

Update `seedContribution` to optionally set ASIN/ReleaseDate/FrontImageURL in the ReleaseInfo. Update `seedUpdateContribution` to put ASIN/date/image in release_info (not match_info), matching the new storage model:

```go
func seedUpdateContribution(t *testing.T, store *db.Store, matchInfo MatchInfo, labels []TitleLabel) int64 {
	t.Helper()

	scan := testScan()
	scanData, err := json.Marshal(scan)
	if err != nil {
		t.Fatalf("marshal scan: %v", err)
	}

	miJSON, err := json.Marshal(matchInfo)
	if err != nil {
		t.Fatalf("marshal match_info: %v", err)
	}

	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		t.Fatalf("marshal labels: %v", err)
	}

	c := db.Contribution{
		DiscKey:          "update-disc-key",
		DiscName:         "THE_MATRIX",
		RawOutput:        scan.RawOutput,
		ScanJSON:         string(scanData),
		ContributionType: "update",
		MatchInfo:        string(miJSON),
		TitleLabels:      string(labelsJSON),
	}
	id, err := store.SaveContribution(c)
	if err != nil {
		t.Fatalf("SaveContribution: %v", err)
	}
	return id
}
```

Note: MatchInfo no longer has ASIN/ReleaseDate/FrontImageURL fields. If a test needs those, set them in release_info via `UpdateContributionDraft()` after seeding.

- [ ] **Step 2: Update all Submit() calls to Execute()**

Find-replace pattern across all tests:
- `svc.Submit(ctx, id, "The Matrix", 1999, "movie")` → `svc.Execute(ctx, id)`
- `svc.Resubmit(ctx, id, "The Matrix", 1999, "movie")` → `svc.Execute(ctx, id)` (after setting status to "submitted")
- `svc.SubmitUpdate(ctx, id)` → `svc.Execute(ctx, id)`
- `svc.ResubmitUpdate(ctx, id)` → `svc.Execute(ctx, id)` (after setting status to "submitted")

For resubmit tests, ensure the contribution is first marked as "submitted" before calling Execute again:
```go
store.UpdateContributionStatus(id, "submitted", "https://github.com/TheDiscDb/data/pull/1")
```

Execute now returns `(string, error)` for all cases (Resubmit used to return just `error`). Update assertions accordingly.

- [ ] **Step 3: Update MatchInfo references in tests**

Remove any test code that sets `ASIN`, `ReleaseDate`, or `FrontImageURL` on `MatchInfo`. Instead, set these on `ReleaseInfo` and store via `UpdateContributionDraft()`.

For example, a test that previously did:
```go
mi := MatchInfo{..., ASIN: "B0CCZQNJ3R", FrontImageURL: "https://example.com/front.jpg"}
id := seedUpdateContribution(t, store, mi, labels)
```

Should become:
```go
mi := MatchInfo{...}  // No ASIN/FrontImageURL
id := seedUpdateContribution(t, store, mi, labels)
ri := ReleaseInfo{ASIN: "B0CCZQNJ3R", FrontImageURL: "https://example.com/front.jpg"}
riJSON, _ := json.Marshal(ri)
store.UpdateContributionDraft(id, "", string(riJSON), "")
```

- [ ] **Step 4: Run all tests**

```bash
go test ./... 2>&1
```

Expected: All tests pass. Fix any remaining failures.

- [ ] **Step 5: Run vet**

```bash
go vet ./...
```

Expected: Clean.

- [ ] **Step 6: Commit**

```bash
git add internal/contribute/service_test.go
git commit -m "test(contribute): update all tests for unified Execute() API"
```

---

## Task 7: Final Verification

- [ ] **Step 1: Full test suite**

```bash
go test ./... -count=1
```

Expected: All pass (no cached results).

- [ ] **Step 2: Race detector**

```bash
go test ./... -race
```

Expected: No races detected.

- [ ] **Step 3: Build**

```bash
go build -o bluforge .
```

Expected: Clean build.

- [ ] **Step 4: Commit any remaining fixes**

If any tests needed fixing, commit them now.
