# TheDiscDB Contribution Feature — Design Spec

## Overview

When BluForge scans a disc that isn't found in TheDiscDB, users can contribute the disc data back by submitting a GitHub PR to `TheDiscDb/data`. BluForge captures the raw scan data automatically, then provides a form where the user labels titles and provides release metadata. On submit, BluForge forks the repo (if needed), generates the required files, and opens a PR.

## Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Authentication | PAT first, GitHub Device Flow later | PAT is simple and works behind any network (Tailscale, Docker). Device Flow is the polished upgrade for users who don't want to manage tokens. |
| UX trigger | Post-rip notification + dedicated contribution queue page | No friction during ripping. Notification gives immediate awareness; queue page lets users come back on their own schedule. |
| Title labeling | Manual — no auto-fill heuristics | Edge cases too varied for meaningful accuracy. Bad guesses are worse than blank fields. |
| TMDB identification | User provides TMDB URL or ID manually | No TMDB API dependency. Simple text input. |
| Data capture | Always store raw MakeMKV output on DiscScan; persist to DB only for unmatched discs | Disc may be gone by contribution time. Raw output stored in contributions table, not every scan. |
| Contribution queue scope | Only unmatched discs | Clear call to action. No noise from already-matched discs. |
| GitHub PR flow | Fork once to user's GitHub account, reuse for subsequent contributions | Standard open-source contribution pattern. No surprise org-level forks. |
| Form layout | Hybrid single-page — release info at top, title labeling below, sticky submit | One page, visually sectioned. No wizard back/forth. |
| GitHub client library | `google/go-github` + `golang.org/x/oauth2` | BSD 3-Clause licensed, compatible with MIT. De facto Go GitHub client. Thin wrapper in `internal/github`. |

## Architecture

### New Packages

**`internal/github`** — Thin wrapper around `google/go-github`.

- Constructs client from PAT via `oauth2.StaticTokenSource`
- Interface-based for mock injection in tests
- Methods:
  - `GetUser(ctx)` — authenticated user's GitHub username
  - `EnsureFork(ctx, owner, repo)` — fork to user's account if not already forked
  - `CreateBranch(ctx, fork, baseSHA, branchName)` — create contribution branch
  - `CommitFiles(ctx, fork, branch, files, message)` — multi-file commit via Git Trees/Blobs API
  - `CreatePR(ctx, upstream, fork, branch, title, body)` — open PR against upstream

**`internal/contribute`** — Core business logic for the contribution lifecycle.

- Dependencies: `internal/github` client interface + `*db.Store` via deps struct
- Responsibilities:
  - **File generation**: Builds TheDiscDB file tree from stored scan data
  - **State management**: CRUD operations on contributions table
  - **Slug generation**: Derives directory/file names matching TheDiscDB conventions
  - **Submit orchestration**: Validates → gets user → ensures fork → generates files → creates branch → commits → opens PR → updates status

### Modified Packages

**`internal/makemkv`** — `DiscScan` gains a `RawOutput string` field. In `ScanDisc`, the raw runner output is captured before being passed to `ParseAll`.

**`internal/workflow`** — `orchestrator.autoMatch()` inserts a `contributions` row when falling through to `unmatchedTitles()`. SSE broadcasts `contribution_available`.

**`internal/config`** — New `GitHubToken string` field for PAT storage. Existing `GitHubClientID`/`GitHubClientSecret` remain for future Device Flow.

**`internal/web`** — New handlers and templates for the contribution pages. Existing `/drives/:id/contribute` stub replaced.

**`internal/db`** — New `contributions` table and Store methods.

## Database

### New Table: `contributions`

```sql
CREATE TABLE IF NOT EXISTS contributions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    disc_key      TEXT UNIQUE NOT NULL,
    disc_name     TEXT NOT NULL,
    raw_output    TEXT NOT NULL,
    scan_json     TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'pending',
    pr_url        TEXT DEFAULT '',
    tmdb_id       TEXT DEFAULT '',
    release_info  TEXT DEFAULT '',
    title_labels  TEXT DEFAULT '',
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

- `disc_key`: SHA256 hash, same as `disc_mappings`. Unique constraint prevents duplicate contributions for the same disc.
- `raw_output`: Full `makemkvcon` robot-mode output (~50-100KB for UHD).
- `scan_json`: Serialized `DiscScan` struct (titles, streams, attributes).
- `release_info`: JSON blob — `{"upc": "", "region_code": "", "year": 0, "format": "", "slug": ""}`.
- `title_labels`: JSON array — `[{"title_index": 0, "type": "MainMovie", "name": "...", "season": "", "episode": "", "file_name": "..."}]`.

## File Generation

BluForge generates four files per disc, matching TheDiscDB's expected structure:

### Directory: `<type>/<Title (Year)>/<release-slug>/`

Where `<type>` is `movie` or `series` based on the TMDB entry.

### `release.json`

```json
{
  "Slug": "2024-blu-ray",
  "Upc": "191329125670",
  "Year": 2024,
  "Locale": "en-us",
  "RegionCode": "1",
  "Title": "2024 Blu-Ray",
  "DateAdded": "2026-04-04T00:00:00Z",
  "Contributors": [
    {"Name": "github-username", "Source": "github"}
  ]
}
```

### `disc01.json`

Mapped from BluForge's `DiscScan` into TheDiscDB's schema:

```json
{
  "Index": 1,
  "Slug": "blu-ray",
  "Name": "Blu-ray",
  "Format": "Blu-ray",
  "ContentHash": "CA4A704D1ECA98D303E0952BF8CB78C6",
  "Titles": [
    {
      "Index": 0,
      "Comment": "...",
      "SourceFile": "00000.mpls",
      "SegmentMap": "0",
      "Duration": "1:45:32",
      "Size": 34567890123,
      "DisplaySize": "32.2 GB",
      "Tracks": [
        {"Index": 0, "Name": "Mpeg4 AVC High@L4.1", "Type": "Video", "Resolution": "1920x1080"},
        {"Index": 1, "Name": "TrueHD Atmos", "Type": "Audio", "LanguageCode": "eng", "Language": "English"}
      ]
    }
  ]
}
```

### `disc01.txt`

Raw `makemkvcon` robot-mode output, stored verbatim from the scan.

### `disc01-summary.txt`

Generated from user's title labels:

```
Name: Main Feature
Source file name: 00800.mpls
Duration: 1:45:32
Chapters count: 28
Size: 32.2 GB
Segment count: 1
Segment Map: 0
Type: MainMovie
File name: Main Feature.mkv

Name: Behind the Scenes
Source file name: 00801.mpls
Duration: 0:23:15
Chapters count: 5
Size: 4.1 GB
Segment count: 1
Segment Map: 1
Type: Extra
File name: Behind the Scenes.mkv
```

Episode entries additionally include `Season` and `Episode` fields.

## Web UI

### Routes

| Method | Path | Handler | Purpose |
|--------|------|---------|---------|
| `GET` | `/contributions` | `handleContributions` | Queue page — lists pending contributions |
| `GET` | `/contributions/:id` | `handleContributionDetail` | Contribution form |
| `POST` | `/contributions/:id` | `handleContributionSave` | Save draft |
| `POST` | `/contributions/:id/submit` | `handleContributionSubmit` | Trigger GitHub PR flow |

Replaces the existing `/drives/:id/contribute` informational stub.

### Contribution Queue Page (`/contributions`)

- Lists unmatched discs: disc name, scan date, status
- Each row links to `/contributions/:id`
- Submitted contributions show PR URL as a clickable link
- Nav bar gets a "Contributions" link

### Contribution Form (`/contributions/:id`)

**Top section — Release info:**
- TMDB URL or ID (text input)
- UPC (text input, optional)
- Region code (dropdown: 1/A, 2/B, 3/C)
- Release year (number input)
- Disc format (dropdown: Blu-ray, UHD, DVD)

**Bottom section — Title labeling:**
- Table of all titles from the scan
- Columns: Source File, Duration, Size, Type (dropdown: MainMovie / Episode / Extra / Trailer / DeletedScene), Name (text), Season (number), Episode (number)
- Season/Episode fields only visible when Type is `Episode`

**Sticky footer:**
- Save Draft button
- Submit to TheDiscDB button (disabled until TMDB ID provided and every title has a Type)

**Tech:** Templ templates + Alpine.js for form interactivity (show/hide season/episode, validation state). Plain form POST for save/submit.

### Post-Rip Notification

- SSE event `contribution_available` broadcast when unmatched disc rip completes
- Dashboard/activity shows dismissible banner: "This disc isn't in TheDiscDB yet. Contribute it"

## End-to-End Flow

1. **Disc inserted → scan → no match**: `autoMatch()` falls through, inserts `contributions` row with `status=pending`. Rip proceeds with generic naming. SSE broadcasts `contribution_available`.

2. **User sees notification**: Banner on dashboard/activity, or navigates to `/contributions`.

3. **User fills form**: Enters TMDB ID, release info, labels each title. Can save draft and return later.

4. **Submit**: Validates fields → `github.GetUser()` → `github.EnsureFork("TheDiscDb", "data")` → generates file tree → `github.CreateBranch(fork, ...)` on branch `contribution/<title-slug>/<release-slug>` → `github.CommitFiles(...)` ��� `github.CreatePR(...)` against `TheDiscDb/data:master` → updates status to `submitted`, stores PR URL. SSE broadcasts `contribution_submitted`.

5. **After submission**: Queue page shows PR link. User tracks review on GitHub. BluForge does not poll merge status.

## Out of Scope

- `metadata.json` / `tmdb.json` generation — TheDiscDB's CI or maintainer handles TMDB metadata enrichment
- Cover art upload — user can add via the GitHub PR directly
- GitHub Device Flow auth — future enhancement, PAT is the first implementation
- Polling PR merge status — the PR link is the deliverable
- Auto-fill heuristics for title labels — too many edge cases for reliable accuracy

## Dependencies

- `github.com/google/go-github/v72` (BSD 3-Clause)
- `golang.org/x/oauth2` (BSD 3-Clause)
